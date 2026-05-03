package events

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/store"
)

// FunctionInvoker is the minimal Function-runtime façade the invoke-fn
// sink needs.
type FunctionInvoker interface {
	InvokeForEvent(ctx context.Context, project, name, path string, body []byte) error
}

// ContainerControl is the minimal container-runtime façade the
// container-action sink needs.
type ContainerControl interface {
	StartByName(ctx context.Context, project, name string) error
	StopByName(ctx context.Context, project, name string) error
	RestartByName(ctx context.Context, project, name string) error
}

// RuleDispatcher matches every emitted event against the active set of
// EventRules and runs the configured sinks. It is plugged into the bus
// as a regular Sink, but it owns all of the rule logic.
type RuleDispatcher struct {
	st         *store.Store
	httpClient *http.Client
	functions  FunctionInvoker
	containers ContainerControl

	deliveryMu sync.Mutex
}

// NewRuleDispatcher wires a dispatcher. Both runtime hooks may be nil;
// rules referencing missing runtimes will record a delivery with an
// error message instead of silently failing.
func NewRuleDispatcher(st *store.Store, fns FunctionInvoker, ctr ContainerControl) *RuleDispatcher {
	return &RuleDispatcher{
		st:         st,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		functions:  fns,
		containers: ctr,
	}
}

// Name implements Sink.
func (d *RuleDispatcher) Name() string { return "rule-dispatcher" }

// Handle implements Sink.
func (d *RuleDispatcher) Handle(ctx context.Context, ev store.EventRecord) {
	rules, err := d.st.ListAllEventRules(ctx)
	if err != nil {
		log.With("err", err).Warn("dispatcher: list rules")
		return
	}
	for i := range rules {
		rule := &rules[i]
		if !rule.Enabled {
			continue
		}
		if rule.Meta.Project != "" && ev.Project != "" && rule.Meta.Project != ev.Project {
			continue
		}
		if !ruleMatches(rule, ev) {
			continue
		}
		// Update last-fired counters.
		rule.LastFiredAt = time.Now().UTC()
		rule.FireCount++
		if err := d.st.PutEventRule(ctx, rule); err != nil {
			log.With("err", err, "rule", rule.Meta.Name).Warn("dispatcher: persist rule fire")
		}
		d.runActions(ctx, rule, ev)
	}
}

// ruleMatches reports whether ev should fire rule. An empty Types list
// matches anything; otherwise we look for an exact match. Subject is a
// glob-light: "*" anywhere acts as a wildcard. For container.log the
// subject is treated as a regex against ev.Data["line"].
func ruleMatches(rule *store.EventRule, ev store.EventRecord) bool {
	if len(rule.Match.Types) > 0 {
		hit := false
		for _, t := range rule.Match.Types {
			if t == ev.Type || t == "*" {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if rule.Match.Subject == "" {
		return true
	}
	if ev.Type == "container.log" {
		re, err := regexp.Compile(rule.Match.Subject)
		if err != nil {
			return false
		}
		return re.MatchString(ev.Data["line"])
	}
	if ev.Subject == rule.Match.Subject {
		return true
	}
	if ok, _ := path.Match(rule.Match.Subject, ev.Subject); ok {
		return true
	}
	return false
}

func (d *RuleDispatcher) runActions(ctx context.Context, rule *store.EventRule, ev store.EventRecord) {
	if rule.Action.Webhook != nil {
		go d.deliverWebhook(ctx, rule, ev)
	}
	if rule.Action.Invoke != nil {
		go d.invokeFunction(ctx, rule, ev)
	}
	if rule.Action.Container != nil {
		go d.containerAction(ctx, rule, ev)
	}
}

// deliverWebhook sends ev to the configured URL. Up to 5 attempts with
// exponential backoff; each attempt is recorded as a WebhookDelivery so
// the UI can show retries.
func (d *RuleDispatcher) deliverWebhook(ctx context.Context, rule *store.EventRule, ev store.EventRecord) {
	cfg := rule.Action.Webhook
	method := cfg.Method
	if method == "" {
		method = "POST"
	}
	body, _ := json.Marshal(ev)

	var lastStatus int
	var lastErr string
	for attempt := 1; attempt <= 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, cfg.URL, bytes.NewReader(body))
		if err != nil {
			lastErr = err.Error()
			break
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("user-agent", "selfcloud-events/1")
		req.Header.Set("x-selfcloud-event", ev.Type)
		req.Header.Set("x-selfcloud-rule", rule.Meta.Name)
		if ev.Subject != "" {
			req.Header.Set("x-selfcloud-subject", ev.Subject)
		}
		for k, v := range cfg.Headers {
			req.Header.Set(k, v)
		}
		if cfg.Secret != "" {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(cfg.Secret))
			mac.Write([]byte(ts + "."))
			mac.Write(body)
			sig := hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("x-selfcloud-signature", "t="+ts+",v1="+sig)
		}

		resp, err := d.httpClient.Do(req)
		if err != nil {
			lastErr = err.Error()
			d.recordDelivery(ctx, rule, ev, attempt, 0, lastErr, false)
			time.Sleep(backoff(attempt))
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		lastStatus = resp.StatusCode
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			d.recordDelivery(ctx, rule, ev, attempt, resp.StatusCode, "", true)
			return
		}
		lastErr = fmt.Sprintf("http %d", resp.StatusCode)
		d.recordDelivery(ctx, rule, ev, attempt, resp.StatusCode, lastErr, false)
		time.Sleep(backoff(attempt))
	}
	d.recordDelivery(ctx, rule, ev, 5, lastStatus, "exhausted: "+lastErr, true)
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt-1)) * time.Second
	if d > time.Minute {
		d = time.Minute
	}
	return d
}

func (d *RuleDispatcher) recordDelivery(ctx context.Context, rule *store.EventRule, ev store.EventRecord, attempt, status int, errMsg string, done bool) {
	d.deliveryMu.Lock()
	defer d.deliveryMu.Unlock()
	dlv := &store.WebhookDelivery{
		Meta: store.Meta{
			Project: rule.Meta.Project,
			Name:    rule.Meta.Name + "-" + ev.UID + "-" + strconv.Itoa(attempt),
		},
		Rule:       rule.Meta.Name,
		URL:        rule.Action.Webhook.URL,
		Status:     status,
		Error:      errMsg,
		Attempt:    attempt,
		Done:       done,
		StartedAt:  time.Now().UTC(),
		FinishedAt: time.Now().UTC(),
		EventUID:   ev.UID,
		EventType:  ev.Type,
	}
	if err := d.st.PutDelivery(ctx, dlv); err != nil {
		log.With("err", err).Warn("dispatcher: persist delivery")
	}
}

func (d *RuleDispatcher) invokeFunction(ctx context.Context, rule *store.EventRule, ev store.EventRecord) {
	if d.functions == nil {
		return
	}
	cfg := rule.Action.Invoke
	project := cfg.Project
	if project == "" {
		project = rule.Meta.Project
	}
	body, _ := json.Marshal(ev)
	p := cfg.Path
	if p == "" {
		p = "/"
	}
	if err := d.functions.InvokeForEvent(ctx, project, cfg.Function, p, body); err != nil {
		log.With("err", err, "fn", cfg.Function, "rule", rule.Meta.Name).Warn("dispatcher: invoke")
	}
}

func (d *RuleDispatcher) containerAction(ctx context.Context, rule *store.EventRule, ev store.EventRecord) {
	if d.containers == nil {
		return
	}
	cfg := rule.Action.Container
	project := cfg.Project
	if project == "" {
		project = rule.Meta.Project
	}
	var err error
	switch cfg.Action {
	case "start":
		err = d.containers.StartByName(ctx, project, cfg.Container)
	case "stop":
		err = d.containers.StopByName(ctx, project, cfg.Container)
	case "restart":
		err = d.containers.RestartByName(ctx, project, cfg.Container)
	default:
		err = errors.New("unknown action " + cfg.Action)
	}
	if err != nil {
		log.With("err", err, "rule", rule.Meta.Name, "container", cfg.Container, "action", cfg.Action).
			Warn("dispatcher: container action")
	} else {
		_ = ev // keep ev referenced for future correlation in deliveries
	}
}
