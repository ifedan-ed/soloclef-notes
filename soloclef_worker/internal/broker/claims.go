package broker

import "sync"

// claimSet is every message this worker holds unacked. The renewer walks it to
// keep each claim fresh, so a job that legitimately runs for hours is never
// mistaken for one a dead worker took.
type claimSet struct {
	mu   sync.Mutex
	tags map[string]struct{}
}

func newClaimSet() *claimSet {
	return &claimSet{tags: map[string]struct{}{}}
}

func (c *claimSet) add(tag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tags[tag] = struct{}{}
}

func (c *claimSet) drop(tag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tags, tag)
}

func (c *claimSet) held() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	tags := make([]string, 0, len(c.tags))
	for tag := range c.tags {
		tags = append(tags, tag)
	}
	return tags
}

func (c *claimSet) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tags)
}
