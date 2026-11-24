package payment

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// jsonClone deep-copies a record through JSON.
func jsonClone[T any](v *T) *T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return &out
}

func matches(search string, fields ...string) bool {
	search = strings.ToLower(strings.TrimSpace(search))
	return search == "" || strings.Contains(strings.ToLower(strings.Join(fields, " ")), search)
}

func (s *MemoryStore) CreateMethod(_ context.Context, m *PaymentMethod) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.methods[m.ID]; ok {
		return errors.New("payment: duplicate id")
	}
	s.methods[m.ID] = jsonClone(m)
	return nil
}

func (s *MemoryStore) UpdateMethod(_ context.Context, m *PaymentMethod) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.methods[m.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Version != m.Version {
		return ErrVersionConflict
	}
	m.Version++
	s.methods[m.ID] = jsonClone(m)
	return nil
}

func (s *MemoryStore) Method(_ context.Context, id string) (*PaymentMethod, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.methods[id]
	if !ok {
		return nil, ErrNotFound
	}
	return jsonClone(m), nil
}

func (s *MemoryStore) Methods(_ context.Context, q MethodQuery) ([]*PaymentMethod, int, error) {
	s.mu.RLock()
	var all []*PaymentMethod
	for _, m := range s.methods {
		if q.Status != "" && m.Status != q.Status || q.GatewayID != "" && m.GatewayID != q.GatewayID ||
			q.CustomerRef != "" && m.CustomerRef != q.CustomerRef ||
			!matches(q.Search, m.ID, m.CustomerRef, m.CustomerEmail, m.CustomerName, m.Last4, m.GatewayRef) {
			continue
		}
		all = append(all, jsonClone(m))
	}
	s.mu.RUnlock()
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return paginate(all, q.Page, q.PerPage), len(all), nil
}

func (s *MemoryStore) CreateSubscription(_ context.Context, sub *Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[sub.ID]; ok {
		return errors.New("payment: duplicate id")
	}
	s.subs[sub.ID] = jsonClone(sub)
	return nil
}

func (s *MemoryStore) UpdateSubscription(_ context.Context, sub *Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.subs[sub.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Version != sub.Version {
		return ErrVersionConflict
	}
	sub.Version++
	s.subs[sub.ID] = jsonClone(sub)
	return nil
}

func (s *MemoryStore) Subscription(_ context.Context, id string) (*Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.subs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return jsonClone(sub), nil
}

func (s *MemoryStore) SubscriptionByGatewayRef(_ context.Context, gatewayID, ref string) (*Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sub := range s.subs {
		if ref != "" && sub.GatewayID == gatewayID && sub.GatewayRef == ref {
			return jsonClone(sub), nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) Subscriptions(_ context.Context, q SubscriptionQuery) ([]*Subscription, int, error) {
	s.mu.RLock()
	var all []*Subscription
	for _, sub := range s.subs {
		if q.Status != "" && sub.Status != q.Status || q.GatewayID != "" && sub.GatewayID != q.GatewayID ||
			q.Reference != "" && sub.Reference != q.Reference || q.CustomerRef != "" && sub.CustomerRef != q.CustomerRef ||
			q.Billing != "" && sub.Billing != q.Billing ||
			!matches(q.Search, sub.ID, sub.Reference, sub.GatewayRef, sub.CustomerEmail, sub.CustomerName, sub.CustomerRef) {
			continue
		}
		all = append(all, jsonClone(sub))
	}
	s.mu.RUnlock()
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return paginate(all, q.Page, q.PerPage), len(all), nil
}

func (s *MemoryStore) DueSubscriptions(_ context.Context, now time.Time, limit int) ([]*Subscription, error) {
	s.mu.RLock()
	var out []*Subscription
	for _, sub := range s.subs {
		if sub.Billing == BillingManager && (sub.Status == SubActive || sub.Status == SubPastDue) &&
			sub.NextBillingAt != nil && !sub.NextBillingAt.After(now) {
			out = append(out, jsonClone(sub))
		}
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].NextBillingAt.Before(*out[j].NextBillingAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
