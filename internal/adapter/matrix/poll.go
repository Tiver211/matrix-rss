package matrix

import (
	"fmt"
	"strings"

	"maunium.net/go/mautrix/event"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// pollTextKeys name the text of a poll question or answer in the stable and unstable formats.
var pollTextKeys = []string{"m.text", "org.matrix.msc1767.text", "body"}

// pollMessage turns a poll start event into a message whose text lists the question and the answers.
// Votes and results are not tracked.
func pollMessage(evt *event.Event, msg domain.Message) (domain.Message, bool) {
	raw := evt.Content.Raw
	msg.Kind = domain.KindPoll
	msg.Body = pollBody(raw)
	if msg.Body == "" {
		return domain.Message{}, false
	}
	if rel, ok := raw["m.relates_to"].(map[string]any); ok {
		if reply, ok := rel["m.in_reply_to"].(map[string]any); ok {
			msg.ReplyTo, _ = reply["event_id"].(string)
		}
	}
	return msg, true
}

func pollBody(raw map[string]any) string {
	poll, _ := raw["m.poll"].(map[string]any)
	if poll == nil {
		poll, _ = raw["org.matrix.msc3381.poll.start"].(map[string]any)
	}
	if poll != nil {
		if question := textOf(poll["question"]); question != "" {
			lines := []string{question}
			answers, _ := poll["answers"].([]any)
			for i, answer := range answers {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, textOf(answer)))
			}
			return strings.Join(lines, "\n")
		}
	}
	// Extensible events carry a text fallback of the whole poll at the top level.
	return textOf(raw)
}

// textOf reads a text from an object that holds it under one of the known keys, either as a plain
// string or as a list of {"body": "..."} variants.
func textOf(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range pollTextKeys {
		switch text := object[key].(type) {
		case string:
			return text
		case []any:
			for _, variant := range text {
				if v, ok := variant.(map[string]any); ok {
					if body, ok := v["body"].(string); ok {
						return body
					}
				}
			}
		}
	}
	return ""
}
