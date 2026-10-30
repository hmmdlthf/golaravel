package broadcast

import (
	stdContext "context"
	"fmt"
	"strings"

	"entgo.io/ent"
)

// EntMutationHook creates an Ent lifecycle hook that automatically broadcasts
// real-time WebSocket and SSE events whenever Ent models are created, updated, or deleted.
func EntMutationHook(channel string, h ...*Hub) ent.Hook {
	hub := DefaultHub()
	if len(h) > 0 && h[0] != nil {
		hub = h[0]
	}

	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx stdContext.Context, m ent.Mutation) (ent.Value, error) {
			val, err := next.Mutate(ctx, m)
			if err != nil {
				return nil, err
			}

			entityType := strings.ToLower(m.Type())
			op := m.Op()

			var event string
			switch {
			case op.Is(ent.OpCreate):
				event = entityType + ".created"
			case op.Is(ent.OpUpdate) || op.Is(ent.OpUpdateOne):
				event = entityType + ".updated"
			case op.Is(ent.OpDelete) || op.Is(ent.OpDeleteOne):
				event = entityType + ".deleted"
			default:
				event = entityType + ".mutated"
			}

			payload := map[string]any{
				"entity": entityType,
				"action": event,
				"op":     op.String(),
			}

			// Capture entity ID if available via Field("id") or ID() interface
			var idVal any
			if idField, ok := m.Field("id"); ok {
				idVal = idField
			} else if idObj, ok := m.(interface{ ID() (int, bool) }); ok {
				if id, exists := idObj.ID(); exists {
					idVal = id
				}
			}

			if idVal != nil {
				payload["id"] = idVal
			}

			// Broadcast to the main channel (e.g. "invoices")
			hub.Broadcast(channel, event, payload)

			// Also broadcast to specific entity instance channel if ID exists (e.g. "invoices.42")
			if idVal != nil {
				hub.Broadcast(fmt.Sprintf("%s.%v", channel, idVal), event, payload)
			}

			return val, nil
		})
	}
}
