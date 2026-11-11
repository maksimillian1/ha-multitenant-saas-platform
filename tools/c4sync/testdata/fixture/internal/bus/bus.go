// Package bus imports app/events while app imports bus: a cycle between
// components that is not a cycle between packages.
package bus

import (
	"fmt"

	"example.com/fixture/internal/app/events"
)

// TopicEvents is a constant topic.
const TopicEvents = "events.v1"

// Client is a message client.
type Client struct{}

// Publish sends an event to a topic.
func (c *Client) Publish(topic string, e events.Event) error { return nil }

// Subscribe listens to topics.
func (c *Client) Subscribe(topics ...string) {}

// Message is a raw message.
type Message struct {
	Topic string
	Body  []byte
}

// Announce uses a topic built at runtime, which is reported as unresolved.
func Announce(c *Client, n int) Message {
	_ = c.Publish(fmt.Sprintf("dyn.%d", n), events.Event{})
	c.Subscribe("audit.v1", TopicEvents)
	return Message{Topic: "dead-letter"}
}
