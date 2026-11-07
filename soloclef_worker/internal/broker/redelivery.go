package broker

import (
	"encoding/json"
	"errors"
	"fmt"
)

// markRedelivered stamps an envelope that is about to go back on its queue,
// setting the two flags kombu's Channel._do_restore_message sets, so a worker
// receiving it a second time can tell.
//
// The envelope is decoded and re-encoded, which reorders its keys. That is
// what the Python transport does to the same message, and nothing reads the
// envelope as bytes, so the content is what has to survive — not the spelling.
func markRedelivered(raw []byte) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("broker: envelope is not a JSON object: %w", err)
	}

	headers, err := objectField(payload, "headers")
	if err != nil {
		return nil, err
	}
	headers["redelivered"] = json.RawMessage(`true`)

	properties, err := objectField(payload, "properties")
	if err != nil {
		return nil, err
	}
	deliveryInfo, err := objectField(properties, "delivery_info")
	if err != nil {
		return nil, err
	}
	deliveryInfo["redelivered"] = json.RawMessage(`true`)

	if err := encodeField(properties, "delivery_info", deliveryInfo); err != nil {
		return nil, err
	}
	if err := encodeField(payload, "headers", headers); err != nil {
		return nil, err
	}
	if err := encodeField(payload, "properties", properties); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

// restoreKey names the Redis list a message goes back to.
//
// Celery publishes these tasks through the anonymous exchange with the queue
// as the routing key, and kombu's own _lookup answers the anonymous exchange
// with the routing key itself, so the routing key the producer recorded is the
// queue. The priority picks which of that queue's lists. Deriving it this way
// rather than reading the binding table is checked against kombu in
// celery_interop_test.go.
func restoreKey(routingKey string, priority int) (string, error) {
	if routingKey == "" {
		return "", errors.New("broker: message has no routing key to be restored to")
	}
	return QueueKey(routingKey, priority), nil
}

func objectField(parent map[string]json.RawMessage, name string) (map[string]json.RawMessage, error) {
	field := map[string]json.RawMessage{}
	raw, present := parent[name]
	if !present {
		return nil, fmt.Errorf("broker: envelope has no %s to stamp", name)
	}
	if err := json.Unmarshal(raw, &field); err != nil {
		return nil, fmt.Errorf("broker: envelope %s is not an object: %w", name, err)
	}
	return field, nil
}

func encodeField(parent map[string]json.RawMessage, name string, field map[string]json.RawMessage) error {
	encoded, err := json.Marshal(field)
	if err != nil {
		return fmt.Errorf("broker: re-encoding %s: %w", name, err)
	}
	parent[name] = encoded
	return nil
}
