package browser

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// Event is a recorded CDP event.
type Event struct {
	Time     time.Time       `json:"time"`
	Category string          `json:"category"` // "console", "network", "page", "target"
	Type     string          `json:"type"`     // e.g. "log", "request", "response", "exception"
	Data     json.RawMessage `json:"data"`
}

// EventBuffer is a fixed-size ring buffer of CDP events.
type EventBuffer struct {
	mu       sync.RWMutex
	events   []Event
	maxBytes int64
	curBytes int64
}

// NewEventBuffer creates a buffer with the given max size in bytes.
func NewEventBuffer(maxBytes int64) *EventBuffer {
	return &EventBuffer{
		maxBytes: maxBytes,
	}
}

func (b *EventBuffer) append(e Event) {
	data, _ := json.Marshal(e)
	size := int64(len(data))

	b.mu.Lock()
	defer b.mu.Unlock()

	// Evict oldest events until we have room
	for b.curBytes+size > b.maxBytes && len(b.events) > 0 {
		old, _ := json.Marshal(b.events[0])
		b.curBytes -= int64(len(old))
		b.events = b.events[1:]
	}

	b.events = append(b.events, e)
	b.curBytes += size
}

// Query returns events matching the given filters.
type EventFilter struct {
	Category string // empty = all
	Type     string // empty = all
	Since    time.Time
	Limit    int // 0 = all
}

func (b *EventBuffer) Query(f EventFilter) []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var results []Event
	for i := len(b.events) - 1; i >= 0; i-- {
		e := b.events[i]
		if !f.Since.IsZero() && e.Time.Before(f.Since) {
			continue
		}
		if f.Category != "" && e.Category != f.Category {
			continue
		}
		if f.Type != "" && e.Type != f.Type {
			continue
		}
		results = append(results, e)
		if f.Limit > 0 && len(results) >= f.Limit {
			break
		}
	}
	// Reverse to chronological order
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}
	return results
}

// Clear removes all events.
func (b *EventBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = nil
	b.curBytes = 0
}

// Stats returns buffer statistics.
func (b *EventBuffer) Stats() map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return map[string]any{
		"count":    len(b.events),
		"bytes":    b.curBytes,
		"maxBytes": b.maxBytes,
	}
}

// StartListening subscribes to CDP events and records them into the buffer.
func StartListening(ctx context.Context, buf *EventBuffer) {
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		now := time.Now()

		switch e := ev.(type) {

		// --- Console ---
		case *runtime.EventConsoleAPICalled:
			args := make([]string, 0, len(e.Args))
			for _, a := range e.Args {
				if a.Value != nil {
					args = append(args, string(a.Value))
				} else if a.Description != "" {
					args = append(args, a.Description)
				} else {
					args = append(args, a.Type.String())
				}
			}
			data, _ := json.Marshal(map[string]any{
				"level": e.Type.String(),
				"args":  args,
			})
			buf.append(Event{Time: now, Category: "console", Type: e.Type.String(), Data: data})

		case *runtime.EventExceptionThrown:
			desc := ""
			if e.ExceptionDetails.Exception != nil {
				desc = e.ExceptionDetails.Exception.Description
			}
			if desc == "" {
				desc = e.ExceptionDetails.Text
			}
			data, _ := json.Marshal(map[string]any{
				"description": desc,
				"line":        e.ExceptionDetails.LineNumber,
				"column":      e.ExceptionDetails.ColumnNumber,
				"url":         e.ExceptionDetails.URL,
			})
			buf.append(Event{Time: now, Category: "console", Type: "exception", Data: data})

		// --- Network ---
		case *network.EventRequestWillBeSent:
			data, _ := json.Marshal(map[string]any{
				"requestId": e.RequestID.String(),
				"method":    e.Request.Method,
				"url":       e.Request.URL,
				"type":      e.Type.String(),
			})
			buf.append(Event{Time: now, Category: "network", Type: "request", Data: data})

		case *network.EventResponseReceived:
			data, _ := json.Marshal(map[string]any{
				"requestId":  e.RequestID.String(),
				"status":     e.Response.Status,
				"statusText": e.Response.StatusText,
				"url":        e.Response.URL,
				"mimeType":   e.Response.MimeType,
			})
			buf.append(Event{Time: now, Category: "network", Type: "response", Data: data})

		case *network.EventLoadingFailed:
			data, _ := json.Marshal(map[string]any{
				"requestId":  e.RequestID.String(),
				"errorText":  e.ErrorText,
				"canceled":   e.Canceled,
				"blockedReason": e.BlockedReason.String(),
			})
			buf.append(Event{Time: now, Category: "network", Type: "failed", Data: data})

		// --- Page ---
		case *page.EventLoadEventFired:
			data, _ := json.Marshal(map[string]any{
				"timestamp": e.Timestamp.Time().String(),
			})
			buf.append(Event{Time: now, Category: "page", Type: "load", Data: data})

		case *page.EventNavigatedWithinDocument:
			data, _ := json.Marshal(map[string]any{
				"url": e.URL,
			})
			buf.append(Event{Time: now, Category: "page", Type: "navigated", Data: data})

		case *page.EventJavascriptDialogOpening:
			data, _ := json.Marshal(map[string]any{
				"dialogType": e.Type.String(),
				"message":    e.Message,
				"url":        e.URL,
			})
			buf.append(Event{Time: now, Category: "page", Type: "dialog", Data: data})

		case *page.EventFrameNavigated:
			data, _ := json.Marshal(map[string]any{
				"frameId": e.Frame.ID.String(),
				"url":     e.Frame.URL,
				"name":    e.Frame.Name,
			})
			buf.append(Event{Time: now, Category: "page", Type: "frame_navigated", Data: data})

		// --- Target ---
		case *target.EventTargetCreated:
			data, _ := json.Marshal(map[string]any{
				"targetId": e.TargetInfo.TargetID.String(),
				"type":     e.TargetInfo.Type,
				"url":      e.TargetInfo.URL,
				"title":    e.TargetInfo.Title,
			})
			buf.append(Event{Time: now, Category: "target", Type: "created", Data: data})

		case *target.EventTargetDestroyed:
			data, _ := json.Marshal(map[string]any{
				"targetId": string(e.TargetID),
			})
			buf.append(Event{Time: now, Category: "target", Type: "destroyed", Data: data})

		case *target.EventTargetInfoChanged:
			data, _ := json.Marshal(map[string]any{
				"targetId": e.TargetInfo.TargetID.String(),
				"type":     e.TargetInfo.Type,
				"url":      e.TargetInfo.URL,
				"title":    e.TargetInfo.Title,
			})
			buf.append(Event{Time: now, Category: "target", Type: "changed", Data: data})
		}
	})
}
