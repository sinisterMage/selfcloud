package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// rootBuckets holds the top-level BoltDB buckets created on first open.
var rootBuckets = []string{
	"projects", "nodes", "containers", "functions",
	"buckets", "accesskeys", "users", "tokens", "cluster",
	"meta",
}

// Store is the persistent state machine. It is backed by BoltDB and intended
// to be wrapped by a Raft FSM (see fsm.go) so that all writes go through the
// log on multi-node clusters. On a single node the FSM short-circuits to
// direct writes.
type Store struct {
	db   *bolt.DB
	mu   sync.RWMutex
	subs []chan Event
}

// Event is emitted on every successful mutation so the API and reconcile
// loops can react.
type Event struct {
	Kind    Kind        `json:"kind"`
	Op      string      `json:"op"` // "put" | "delete"
	Project string      `json:"project,omitempty"`
	Name    string      `json:"name"`
	Value   interface{} `json:"value,omitempty"`
}

// Open creates or opens the BoltDB at dataDir/state.db.
func Open(dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, "state.db")
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open boltdb: %w", err)
	}
	s := &Store{db: db}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, b := range rootBuckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(b)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close shuts down the underlying database.
func (s *Store) Close() error {
	s.mu.Lock()
	for _, ch := range s.subs {
		close(ch)
	}
	s.subs = nil
	s.mu.Unlock()
	return s.db.Close()
}

// Subscribe returns a channel of events. Caller should drain promptly.
func (s *Store) Subscribe() <-chan Event {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.subs = append(s.subs, ch)
	s.mu.Unlock()
	return ch
}

func (s *Store) emit(ev Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.subs {
		select {
		case ch <- ev:
		default:
			// drop on backpressure rather than block raft
		}
	}
}

// keyFor returns the BoltDB key for a project-scoped resource, or just the
// name for cluster-scoped.
func keyFor(project, name string) []byte {
	if project == "" {
		return []byte(name)
	}
	return []byte(project + "/" + name)
}

func newUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Store) put(bucket string, key []byte, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).Put(key, data)
	})
}

func (s *Store) get(bucket string, key []byte, out any) error {
	return s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bucket)).Get(key)
		if v == nil {
			return ErrNotFound
		}
		return json.Unmarshal(v, out)
	})
}

func (s *Store) del(bucket string, key []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b.Get(key) == nil {
			return ErrNotFound
		}
		return b.Delete(key)
	})
}

func (s *Store) list(bucket string, prefix []byte) ([][]byte, error) {
	var out [][]byte
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucket)).Cursor()
		if len(prefix) == 0 {
			for k, v := c.First(); k != nil; k, v = c.Next() {
				cp := make([]byte, len(v))
				copy(cp, v)
				out = append(out, cp)
			}
			return nil
		}
		for k, v := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = c.Next() {
			cp := make([]byte, len(v))
			copy(cp, v)
			out = append(out, cp)
		}
		return nil
	})
	return out, err
}

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

// ----------------------- Projects -----------------------

func (s *Store) PutProject(ctx context.Context, p *Project) error {
	if p.Meta.Name == "" {
		return fmt.Errorf("project name is required")
	}
	if p.Meta.UID == "" {
		p.Meta.UID = newUID()
		p.Meta.CreatedAt = time.Now().UTC()
	}
	p.Meta.UpdatedAt = time.Now().UTC()
	p.Meta.Generation++
	if err := s.put("projects", []byte(p.Meta.Name), p); err != nil {
		return err
	}
	s.emit(Event{Kind: KindProject, Op: "put", Name: p.Meta.Name, Value: p})
	return nil
}

func (s *Store) GetProject(_ context.Context, name string) (*Project, error) {
	var p Project
	if err := s.get("projects", []byte(name), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListProjects(_ context.Context) ([]Project, error) {
	raws, err := s.list("projects", nil)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(raws))
	for _, r := range raws {
		var p Project
		if err := json.Unmarshal(r, &p); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) DeleteProject(_ context.Context, name string) error {
	if name == "default" {
		return fmt.Errorf("cannot delete the default project")
	}
	if err := s.del("projects", []byte(name)); err != nil {
		return err
	}
	s.emit(Event{Kind: KindProject, Op: "delete", Name: name})
	return nil
}

// ----------------------- generic helpers for project-scoped resources -------

type projectScoped interface {
	getMeta() *Meta
}

func (m *Meta) getMeta() *Meta { return m }

func (c *Container) getMeta() *Meta { return &c.Meta }
func (f *Function) getMeta() *Meta  { return &f.Meta }
func (b *Bucket) getMeta() *Meta    { return &b.Meta }
func (a *AccessKey) getMeta() *Meta { return &a.Meta }
func (n *Node) getMeta() *Meta      { return &n.Meta }
func (u *User) getMeta() *Meta      { return &u.Meta }
func (t *Token) getMeta() *Meta     { return &t.Meta }

func (s *Store) putScoped(bucket string, kind Kind, v projectScoped) error {
	m := v.getMeta()
	if m.Project == "" {
		m.Project = "default"
	}
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	if m.UID == "" {
		m.UID = newUID()
		m.CreatedAt = time.Now().UTC()
	}
	m.UpdatedAt = time.Now().UTC()
	m.Generation++
	if err := s.put(bucket, keyFor(m.Project, m.Name), v); err != nil {
		return err
	}
	s.emit(Event{Kind: kind, Op: "put", Project: m.Project, Name: m.Name, Value: v})
	return nil
}

// ----------------------- Containers ----------------------

func (s *Store) PutContainer(_ context.Context, c *Container) error {
	return s.putScoped("containers", KindContainer, c)
}

func (s *Store) GetContainer(_ context.Context, project, name string) (*Container, error) {
	var c Container
	if err := s.get("containers", keyFor(project, name), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) ListContainers(_ context.Context, project string) ([]Container, error) {
	var prefix []byte
	if project != "" {
		prefix = []byte(project + "/")
	}
	raws, err := s.list("containers", prefix)
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(raws))
	for _, r := range raws {
		var c Container
		if err := json.Unmarshal(r, &c); err == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Store) DeleteContainer(_ context.Context, project, name string) error {
	if err := s.del("containers", keyFor(project, name)); err != nil {
		return err
	}
	s.emit(Event{Kind: KindContainer, Op: "delete", Project: project, Name: name})
	return nil
}

// ----------------------- Functions -----------------------

func (s *Store) PutFunction(_ context.Context, f *Function) error {
	return s.putScoped("functions", KindFunction, f)
}

func (s *Store) GetFunction(_ context.Context, project, name string) (*Function, error) {
	var f Function
	if err := s.get("functions", keyFor(project, name), &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) ListFunctions(_ context.Context, project string) ([]Function, error) {
	var prefix []byte
	if project != "" {
		prefix = []byte(project + "/")
	}
	raws, err := s.list("functions", prefix)
	if err != nil {
		return nil, err
	}
	out := make([]Function, 0, len(raws))
	for _, r := range raws {
		var f Function
		if err := json.Unmarshal(r, &f); err == nil {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Store) DeleteFunction(_ context.Context, project, name string) error {
	if err := s.del("functions", keyFor(project, name)); err != nil {
		return err
	}
	s.emit(Event{Kind: KindFunction, Op: "delete", Project: project, Name: name})
	return nil
}

// ----------------------- Buckets -------------------------

func (s *Store) PutBucket(_ context.Context, b *Bucket) error {
	return s.putScoped("buckets", KindBucket, b)
}

func (s *Store) GetBucket(_ context.Context, project, name string) (*Bucket, error) {
	var b Bucket
	if err := s.get("buckets", keyFor(project, name), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) ListBuckets(_ context.Context, project string) ([]Bucket, error) {
	var prefix []byte
	if project != "" {
		prefix = []byte(project + "/")
	}
	raws, err := s.list("buckets", prefix)
	if err != nil {
		return nil, err
	}
	out := make([]Bucket, 0, len(raws))
	for _, r := range raws {
		var b Bucket
		if err := json.Unmarshal(r, &b); err == nil {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *Store) DeleteBucket(_ context.Context, project, name string) error {
	if err := s.del("buckets", keyFor(project, name)); err != nil {
		return err
	}
	s.emit(Event{Kind: KindBucket, Op: "delete", Project: project, Name: name})
	return nil
}

// ----------------------- AccessKeys ---------------------

func (s *Store) PutAccessKey(_ context.Context, a *AccessKey) error {
	return s.putScoped("accesskeys", KindAccessKey, a)
}

func (s *Store) GetAccessKey(_ context.Context, project, name string) (*AccessKey, error) {
	var a AccessKey
	if err := s.get("accesskeys", keyFor(project, name), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListAccessKeys(_ context.Context, project string) ([]AccessKey, error) {
	var prefix []byte
	if project != "" {
		prefix = []byte(project + "/")
	}
	raws, err := s.list("accesskeys", prefix)
	if err != nil {
		return nil, err
	}
	out := make([]AccessKey, 0, len(raws))
	for _, r := range raws {
		var a AccessKey
		if err := json.Unmarshal(r, &a); err == nil {
			a.SecretAccessKey = "" // never expose stored secret on list
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Store) DeleteAccessKey(_ context.Context, project, name string) error {
	if err := s.del("accesskeys", keyFor(project, name)); err != nil {
		return err
	}
	s.emit(Event{Kind: KindAccessKey, Op: "delete", Project: project, Name: name})
	return nil
}

// ----------------------- Nodes ---------------------------

func (s *Store) PutNode(_ context.Context, n *Node) error {
	if n.Meta.Project == "" {
		n.Meta.Project = "system"
	}
	return s.putScoped("nodes", KindNode, n)
}

func (s *Store) GetNode(_ context.Context, id string) (*Node, error) {
	var n Node
	if err := s.get("nodes", []byte("system/"+id), &n); err != nil {
		return nil, err
	}
	return &n, nil
}

func (s *Store) ListNodes(_ context.Context) ([]Node, error) {
	raws, err := s.list("nodes", nil)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(raws))
	for _, r := range raws {
		var n Node
		if err := json.Unmarshal(r, &n); err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *Store) DeleteNode(_ context.Context, id string) error {
	if err := s.del("nodes", []byte("system/"+id)); err != nil {
		return err
	}
	s.emit(Event{Kind: KindNode, Op: "delete", Name: id})
	return nil
}

// ----------------------- Users & Tokens ------------------

func (s *Store) PutUser(_ context.Context, u *User) error {
	if u.Meta.Project == "" {
		u.Meta.Project = "system"
	}
	return s.putScoped("users", KindUser, u)
}

func (s *Store) GetUser(_ context.Context, name string) (*User, error) {
	var u User
	if err := s.get("users", []byte("system/"+name), &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByEmail(_ context.Context, email string) (*User, error) {
	raws, err := s.list("users", nil)
	if err != nil {
		return nil, err
	}
	email = strings.ToLower(email)
	for _, r := range raws {
		var u User
		if err := json.Unmarshal(r, &u); err == nil && strings.ToLower(u.Email) == email {
			return &u, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) ListUsers(_ context.Context) ([]User, error) {
	raws, err := s.list("users", nil)
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(raws))
	for _, r := range raws {
		var u User
		if err := json.Unmarshal(r, &u); err == nil {
			u.PasswordHash = ""
			out = append(out, u)
		}
	}
	return out, nil
}

func (s *Store) DeleteUser(_ context.Context, name string) error {
	if err := s.del("users", []byte("system/"+name)); err != nil {
		return err
	}
	s.emit(Event{Kind: KindUser, Op: "delete", Name: name})
	return nil
}

func (s *Store) PutToken(_ context.Context, t *Token) error {
	if t.Meta.Project == "" {
		t.Meta.Project = "system"
	}
	stored := *t
	stored.Plaintext = "" // never persist plaintext
	if err := s.putScoped("tokens", KindToken, &stored); err != nil {
		return err
	}
	t.Meta = stored.Meta
	return nil
}

func (s *Store) GetTokenByHash(_ context.Context, hashHex string) (*Token, error) {
	raws, err := s.list("tokens", nil)
	if err != nil {
		return nil, err
	}
	for _, r := range raws {
		var t Token
		if err := json.Unmarshal(r, &t); err == nil && t.HashHex == hashHex {
			return &t, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) ListTokens(_ context.Context) ([]Token, error) {
	raws, err := s.list("tokens", nil)
	if err != nil {
		return nil, err
	}
	out := make([]Token, 0, len(raws))
	for _, r := range raws {
		var t Token
		if err := json.Unmarshal(r, &t); err == nil {
			t.HashHex = ""
			t.Plaintext = ""
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Store) DeleteToken(_ context.Context, name string) error {
	if err := s.del("tokens", []byte("system/"+name)); err != nil {
		return err
	}
	s.emit(Event{Kind: KindToken, Op: "delete", Name: name})
	return nil
}

// ----------------------- Cluster config -----------------

func (s *Store) GetCluster(_ context.Context) (*ClusterConfig, error) {
	var c ClusterConfig
	if err := s.get("cluster", []byte("config"), &c); err != nil {
		if err == ErrNotFound {
			return &ClusterConfig{ReplicationFactor: 1}, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s *Store) PutCluster(_ context.Context, c *ClusterConfig) error {
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if err := s.put("cluster", []byte("config"), c); err != nil {
		return err
	}
	s.emit(Event{Kind: KindCluster, Op: "put", Name: "config", Value: c})
	return nil
}
