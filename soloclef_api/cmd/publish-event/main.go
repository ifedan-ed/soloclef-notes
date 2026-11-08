// publish-event states one domain event on the event stream and prints the
// entry id. It exists so the cross-language proof can publish from Go without a
// Go write route having been ported yet (custom/docs/event-bus.md).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/eventstream"
)

func main() {
	redisURL := flag.String("redis-url", "", "the stream's Redis, as REDIS_APP_URL")
	eventType := flag.String("event-type", "", "the dotted event type")
	workspace := flag.Int64("workspace-id", 0, "the workspace the event is scoped to")
	payload := flag.String("payload", "{}", "the event payload, as JSON")
	flag.Parse()

	if err := run(*redisURL, *eventType, *workspace, *payload); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(redisURL, eventType string, workspaceID int64, payload string) error {
	if redisURL == "" || eventType == "" || workspaceID == 0 {
		return fmt.Errorf("-redis-url, -event-type and -workspace-id are all required")
	}
	var decoded json.RawMessage
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return fmt.Errorf("the payload is not JSON: %w", err)
	}
	event, err := eventstream.NewEvent(eventType, decoded, workspaceID, time.Now())
	if err != nil {
		return err
	}
	publisher, err := eventstream.NewPublisher(redisURL)
	if err != nil {
		return err
	}
	defer publisher.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := publisher.Publish(ctx, event)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}
