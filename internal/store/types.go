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
	RestartPolicy RestartPolicy     `json:"restartPolicy"`
	Resources     ResourceLimits    `json:"resources,omitempty"`
	NodeID        string            `json:"nodeId,omitempty"`
	Status        ContainerStatus   `json:"status"`
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
	Meta      Meta              `json:"meta"`
	Runtime   FunctionRuntime   `json:"runtime"`
	Handler   string            `json:"handler,omitempty"`
	SourceRef string            `json:"sourceRef"` // sha256 content-addressed blob in DataDir/functions/blobs/<sha>
	Triggers  []FunctionTrigger `json:"triggers"`
	Env       map[string]string `json:"env,omitempty"`
	MemoryMB  int               `json:"memoryMB,omitempty"`
	TimeoutMS int               `json:"timeoutMs,omitempty"`
	Status    Status            `json:"status"`
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
	CreatedAt         time.Time   `json:"createdAt"`
	UpdatedAt         time.Time   `json:"updatedAt"`
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
