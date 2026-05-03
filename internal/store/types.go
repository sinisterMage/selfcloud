// Package store defines the persistent state machine for selfcloud and the
// resource types that the API, runtimes and Terraform provider all share.
package store

import (
	"errors"
	"time"
)

// Errors returned by the store layer. Mapped to HTTP status codes by the API.
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrConflict      = errors.New("conflict")
	ErrNotLeader     = errors.New("not the raft leader")
)

// Kind is the canonical name of a resource kind. Kept short because it is part
// of every key in BoltDB.
type Kind string

const (
	KindProject   Kind = "project"
	KindNode      Kind = "node"
	KindContainer Kind = "container"
	KindFunction  Kind = "function"
	KindBucket    Kind = "bucket"
	KindAccessKey Kind = "accesskey"
	KindUser      Kind = "user"
	KindToken     Kind = "token"
	KindCluster   Kind = "cluster"
	KindSecret    Kind = "secret"
	KindEventRule Kind = "eventrule"
	KindBuild     Kind = "build"
	KindEvent     Kind = "event"
)

// Meta is shared by every persisted resource.
type Meta struct {
	Project    string            `json:"project"`
	Name       string            `json:"name"`
	UID        string            `json:"uid"`
	Generation int64             `json:"generation"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// Phase is the high-level lifecycle state observed for a resource.
type Phase string

const (
	PhasePending   Phase = "Pending"
	PhaseRunning   Phase = "Running"
	PhaseFailed    Phase = "Failed"
	PhaseSucceeded Phase = "Succeeded"
	PhaseStopped   Phase = "Stopped"
)

// Status is observed state common to most resources.
type Status struct {
	Phase     Phase     `json:"phase"`
	Message   string    `json:"message,omitempty"`
	NodeID    string    `json:"nodeId,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Project is a soft tenancy boundary. Names must be DNS-label compatible.
type Project struct {
	Meta        Meta   `json:"meta"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

// NodeRole indicates which capabilities a node provides.
type NodeRole string

const (
	NodeRoleControl NodeRole = "control"
	NodeRoleCompute NodeRole = "compute"
	NodeRoleStorage NodeRole = "storage"
)

type Node struct {
	Meta          Meta       `json:"meta"`
	Address       string     `json:"address"`
	APIAddress    string     `json:"apiAddress"`
	RaftAddress   string     `json:"raftAddress"`
	GossipAddress string     `json:"gossipAddress"`
	Roles         []NodeRole `json:"roles"`
	CapacityGB    int64      `json:"capacityGB"`
	Zone          string     `json:"zone,omitempty"`
	Version       string     `json:"version"`
	Status        Status     `json:"status"`
	LastSeenAt    time.Time  `json:"lastSeenAt"`
}

type RestartPolicy string

const (
	RestartNever     RestartPolicy = "Never"
	RestartOnFailure RestartPolicy = "OnFailure"
	RestartAlways    RestartPolicy = "Always"
)

type PortMapping struct {
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol"` // tcp or udp
}

type ResourceLimits struct {
	CPUMillicores int64 `json:"cpuMillicores,omitempty"`
	MemoryMB      int64 `json:"memoryMB,omitempty"`
}

type Container struct {
	Meta          Meta              `json:"meta"`
	Image         string            `json:"image"`
	Command       []string          `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Ports         []PortMapping     `json:"ports,omitempty"`
	Volumes       []VolumeMount     `json:"volumes,omitempty"`
	SecretMounts  []SecretMount     `json:"secretMounts,omitempty"`
	RestartPolicy RestartPolicy     `json:"restartPolicy"`
	Resources     ResourceLimits    `json:"resources,omitempty"`
	NodeID        string            `json:"nodeId,omitempty"`
	Status        ContainerStatus   `json:"status"`
}

// SecretMount is a binding from a project-scoped Secret onto a container
// or function. If EnvName is set, the resolved plaintext is injected into
// the environment as that variable. If MountPath is set, the plaintext is
// written to a tmpfs file inside the container at that path.
type SecretMount struct {
	Secret    string `json:"secret"`
	EnvName   string `json:"envName,omitempty"`
	MountPath string `json:"mountPath,omitempty"`
}

// VolumeMount currently supports mounting an S3 bucket as a virtual file
// system (rclone mount under the hood) for convenience.
type VolumeMount struct {
	Bucket    string `json:"bucket,omitempty"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

type ContainerStatus struct {
	Status
	ContainerdID string    `json:"containerdId,omitempty"`
	StartedAt    time.Time `json:"startedAt,omitempty"`
	ExitCode     int       `json:"exitCode,omitempty"`
	IPAddress    string    `json:"ipAddress,omitempty"`
	Image        string    `json:"image,omitempty"`
}

type FunctionRuntime string

const (
	FunctionRuntimeWasm        FunctionRuntime = "wasm"
	FunctionRuntimeFirecracker FunctionRuntime = "firecracker"
)

type FunctionTrigger struct {
	HTTP *HTTPTrigger `json:"http,omitempty"`
	Cron *CronTrigger `json:"cron,omitempty"`
}

type HTTPTrigger struct {
	Path    string   `json:"path"`
	Methods []string `json:"methods,omitempty"`
}

type CronTrigger struct {
	Schedule string `json:"schedule"`
}

type Function struct {
	Meta         Meta              `json:"meta"`
	Runtime      FunctionRuntime   `json:"runtime"`
	Handler      string            `json:"handler,omitempty"`
	SourceRef    string            `json:"sourceRef"` // sha256 content-addressed blob in DataDir/functions/blobs/<sha>
	Source       FunctionSource    `json:"source,omitempty"`
	Triggers     []FunctionTrigger `json:"triggers"`
	Env          map[string]string `json:"env,omitempty"`
	SecretMounts []SecretMount     `json:"secretMounts,omitempty"`
	MemoryMB     int               `json:"memoryMB,omitempty"`
	TimeoutMS    int               `json:"timeoutMs,omitempty"`
	LatestBuild  string            `json:"latestBuild,omitempty"`
	Status       Status            `json:"status"`
}

// FunctionSource describes where the code came from. Type is "upload" for
// the legacy direct upload flow (default), or "git" for repos cloned and
// built on the server.
type FunctionSource struct {
	Type string         `json:"type,omitempty"`
	Git  *GitSourceSpec `json:"git,omitempty"`
}

// GitSourceSpec is the per-function Git deployment config. AuthSecret
// references a project-scoped Secret containing a personal access token
// for cloning private repos. WebhookSecret is the HMAC secret the user
// configures in their Git host's webhook UI.
type GitSourceSpec struct {
	URL           string    `json:"url"`
	Ref           string    `json:"ref,omitempty"`
	SubPath       string    `json:"subPath,omitempty"`
	AuthSecret    string    `json:"authSecret,omitempty"`
	WebhookToken  string    `json:"webhookToken,omitempty"` // url-path-segment, unique per fn
	WebhookSecret string    `json:"webhookSecret,omitempty"`
	Build         BuildSpec `json:"build"`
}

// BuildSpec lets the user override the autodetected build pipeline. With
// Language="auto" and no Commands set we infer everything from files in
// the repo (package.json -> node, Cargo.toml -> rust, ...).
type BuildSpec struct {
	Language   string   `json:"language,omitempty"`
	BuildImage string   `json:"buildImage,omitempty"`
	Commands   []string `json:"commands,omitempty"`
	Output     string   `json:"output,omitempty"`
	Entrypoint []string `json:"entrypoint,omitempty"`
	Template   string   `json:"template,omitempty"`
}

// Build is one execution of the builder. Logs are streamed live to a
// channel and persisted to disk at <dataDir>/functions/build-logs/<uid>.log.
type Build struct {
	Meta        Meta      `json:"meta"`
	FunctionRef string    `json:"functionRef"`
	CommitSHA   string    `json:"commitSha,omitempty"`
	Trigger     string    `json:"trigger,omitempty"` // "manual" | "webhook" | "create"
	Status      Phase     `json:"status"`
	Message     string    `json:"message,omitempty"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
	FinishedAt  time.Time `json:"finishedAt,omitempty"`
	LogsRef     string    `json:"logsRef,omitempty"`    // sha256 of build logs blob
	ArtifactRef string    `json:"artifactRef,omitempty"` // sha256 of produced artifact (= Function.SourceRef)
}

// Secret is a project-scoped, encrypted-at-rest credential. The plaintext
// is never returned by ListSecrets; callers explicitly hit the reveal
// endpoint (admin only) when they need it.
type Secret struct {
	Meta        Meta   `json:"meta"`
	Description string `json:"description,omitempty"`
	KeyID       string `json:"keyId"`
	Nonce       []byte `json:"nonce,omitempty"`
	Ciphertext  []byte `json:"ciphertext,omitempty"`
	Version     int    `json:"version"`
}

// EventRule binds a match pattern to one or more actions. A single rule
// can carry multiple actions; all configured actions fire when the rule
// matches an event.
type EventRule struct {
	Meta        Meta        `json:"meta"`
	Description string      `json:"description,omitempty"`
	Match       EventMatch  `json:"match"`
	Action      EventAction `json:"action"`
	Enabled     bool        `json:"enabled"`
	LastFiredAt time.Time   `json:"lastFiredAt,omitempty"`
	FireCount   int64       `json:"fireCount,omitempty"`
}

// EventMatch describes which events fire this rule. An empty Types list
// matches everything (useful for catch-all log rules). Subject is matched
// as a glob; for log-pattern events it is the regex to match against the
// log line.
type EventMatch struct {
	Types   []string          `json:"types,omitempty"`
	Subject string            `json:"subject,omitempty"`
	Filter  map[string]string `json:"filter,omitempty"`
}

// EventAction carries one or more sinks. The Log sink is implicit: every
// emitted event is appended to the cluster event log regardless of any
// rule.
type EventAction struct {
	Webhook   *WebhookAction   `json:"webhook,omitempty"`
	Invoke    *InvokeAction    `json:"invoke,omitempty"`
	Container *ContainerAction `json:"container,omitempty"`
}

type WebhookAction struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Secret  string            `json:"secret,omitempty"` // HMAC secret; never returned over the wire on list
}

type InvokeAction struct {
	Project  string `json:"project,omitempty"`
	Function string `json:"function"`
	Path     string `json:"path,omitempty"`
}

type ContainerAction struct {
	Project   string `json:"project,omitempty"`
	Container string `json:"container"`
	Action    string `json:"action"` // "start" | "stop" | "restart"
}

// EventRecord is a single observation pushed onto the events bus. It is
// also persisted to a bounded log per project so the dashboard can show a
// timeline. This is distinct from the in-process Event type used by the
// store's pubsub for resource mutations.
type EventRecord struct {
	UID     string            `json:"uid"`
	Type    string            `json:"type"`
	Project string            `json:"project,omitempty"`
	Subject string            `json:"subject,omitempty"`
	At      time.Time         `json:"at"`
	Data    map[string]string `json:"data,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// WebhookDelivery records one outbound webhook attempt. Persisted so the
// UI can show "deliveries" per rule, including retries and errors.
type WebhookDelivery struct {
	Meta       Meta      `json:"meta"`
	Rule       string    `json:"rule"`
	URL        string    `json:"url"`
	Status     int       `json:"status,omitempty"`
	Error      string    `json:"error,omitempty"`
	Attempt    int       `json:"attempt"`
	NextAttempt time.Time `json:"nextAttempt,omitempty"`
	Done       bool      `json:"done"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	EventUID   string    `json:"eventUid,omitempty"`
	EventType  string    `json:"eventType,omitempty"`
}

type Bucket struct {
	Meta          Meta   `json:"meta"`
	Region        string `json:"region,omitempty"`
	Versioning    bool   `json:"versioning,omitempty"`
	WebsiteAccess bool   `json:"websiteAccess,omitempty"`
	GarageID      string `json:"garageId,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Status        Status `json:"status"`
}

type AccessKey struct {
	Meta            Meta   `json:"meta"`
	BucketName      string `json:"bucketName,omitempty"` // empty == cluster-wide
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"` // returned only on creation
	Permissions     string `json:"permissions"`               // "read", "write", "owner"
}

type User struct {
	Meta         Meta      `json:"meta"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"passwordHash,omitempty"`
	IsAdmin      bool      `json:"isAdmin"`
	LastLoginAt  time.Time `json:"lastLoginAt,omitempty"`
}

type Token struct {
	Meta      Meta      `json:"meta"`
	UserID    string    `json:"userId"`
	HashHex   string    `json:"hashHex,omitempty"`
	Plaintext string    `json:"plaintext,omitempty"` // returned only on creation; cleared before persistence by PutToken
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	IsAdmin   bool      `json:"isAdmin,omitempty"`
}

// ClusterConfig is a singleton; encodes wizard answers and global flags.
type ClusterConfig struct {
	Initialized       bool        `json:"initialized"`
	MultiNode         bool        `json:"multiNode"`
	BootstrapToken    string      `json:"bootstrapToken,omitempty"` // hashed
	ReplicationFactor int         `json:"replicationFactor"`
	JoinTokens        []JoinToken `json:"joinTokens,omitempty"`
	GarageRPCSecret   string      `json:"garageRpcSecret,omitempty"`
	GarageAdminToken  string      `json:"garageAdminToken,omitempty"`
	// Internal S3 admin credentials used by the dashboard's bucket
	// browser. Created at first boot. Never returned over the API.
	S3InternalKeyID     string `json:"s3InternalKeyId,omitempty"`
	S3InternalKeySecret string `json:"s3InternalKeySecret,omitempty"`
	// SecretFingerprint is sha256(masterKey)[:8]. Used to detect a
	// swapped or corrupted master.key file on subsequent boots.
	SecretFingerprint string    `json:"secretFingerprint,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// JoinToken is the cluster-wide artefact a node consumes when running
// `selfcloud join`. Stored hashed; plaintext only ever returned to the user
// once at issue time.
type JoinToken struct {
	ID         string    `json:"id"`
	HashHex    string    `json:"hashHex"`
	IssuedBy   string    `json:"issuedBy"`
	IssuedAt   time.Time `json:"issuedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	ConsumedBy string    `json:"consumedBy,omitempty"`
}
