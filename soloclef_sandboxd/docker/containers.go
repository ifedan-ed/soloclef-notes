package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// ContainerSpec is the body of a container's creation: the Engine API's own
// field names, of only the fields this service sets. Everything left out
// takes the daemon's default, so each field here is a decision, explained
// where the spec is built (sandboxes/spec.go).
type ContainerSpec struct {
	Image           string            `json:"Image"`
	Entrypoint      []string          `json:"Entrypoint"`
	Cmd             []string          `json:"Cmd"`
	Env             []string          `json:"Env,omitempty"`
	User            string            `json:"User,omitempty"`
	WorkingDir      string            `json:"WorkingDir,omitempty"`
	Labels          map[string]string `json:"Labels"`
	NetworkDisabled bool              `json:"NetworkDisabled"`
	HostConfig      HostConfig        `json:"HostConfig"`
}

// HostConfig is the part of the spec that confines the container.
type HostConfig struct {
	NetworkMode    string            `json:"NetworkMode"`
	ReadonlyRootfs bool              `json:"ReadonlyRootfs"`
	Tmpfs          map[string]string `json:"Tmpfs,omitempty"`
	CapDrop        []string          `json:"CapDrop"`
	SecurityOpt    []string          `json:"SecurityOpt"`
	Init           bool              `json:"Init"`
	NanoCPUs       int64             `json:"NanoCpus"`
	Memory         int64             `json:"Memory"`
	MemorySwap     int64             `json:"MemorySwap"`
	PidsLimit      int64             `json:"PidsLimit"`
	Ulimits        []Ulimit          `json:"Ulimits,omitempty"`
	ShmSize        int64             `json:"ShmSize,omitempty"`
	LogConfig      LogConfig         `json:"LogConfig"`
	RestartPolicy  RestartPolicy     `json:"RestartPolicy"`
}

// Ulimit is one resource limit, soft and hard.
type Ulimit struct {
	Name string `json:"Name"`
	Soft int64  `json:"Soft"`
	Hard int64  `json:"Hard"`
}

// LogConfig names the container's log driver.
type LogConfig struct {
	Type string `json:"Type"`
}

// RestartPolicy says whether the daemon restarts the container.
type RestartPolicy struct {
	Name string `json:"Name"`
}

// Create creates a container under name and returns its id. A name already
// taken is a conflict (IsConflict).
func (c *Client) Create(ctx context.Context, name string, spec ContainerSpec) (string, error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, spec, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// Start starts a created container.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil, nil)
}

// Remove kills and removes a container with its anonymous volumes. One that
// is already gone is not an error: removing is what the caller wanted.
func (c *Client) Remove(ctx context.Context, id string) error {
	err := c.call(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), url.Values{"force": {"true"}, "v": {"true"}}, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// Container is one container as a list returns it.
type Container struct {
	ID     string            `json:"Id"`
	Labels map[string]string `json:"Labels"`
	// State is "running", "exited", "created" and so on.
	State string `json:"State"`
}

// List returns every container, running or not, carrying all of the labels
// ("key=value" each).
func (c *Client) List(ctx context.Context, labels ...string) ([]Container, error) {
	filters, err := json.Marshal(map[string][]string{"label": labels})
	if err != nil {
		return nil, err
	}
	var found []Container
	err = c.call(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"true"}, "filters": {string(filters)}}, nil, &found)
	return found, err
}
