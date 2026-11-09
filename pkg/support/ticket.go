package support

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Ticket Category enums
const (
	CategorySupport        = "support"
	CategoryBug            = "bug"
	CategoryFeatureRequest = "feature_request"
	CategoryBilling        = "billing"
	CategoryGeneral        = "general"
)

// Ticket Mode enums: AI automated bot or Human live agent.
const (
	ModeAI   = "ai"
	ModeLive = "live"
)

// Ticket Status enums
const (
	StatusOpen      = "open"
	StatusPending   = "pending"
	StatusEscalated = "escalated"
	StatusResolved  = "resolved"
	StatusClosed    = "closed"
)

// Ticket Priority enums
const (
	PriorityLow    = "low"
	PriorityNormal = "normal"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"
)

// Feasibility ratings for feature requests
const (
	FeasibilityExisting    = "existing_feature"
	FeasibilitySimple      = "simple"
	FeasibilityComplex     = "complex"
	FeasibilityUnfeasible  = "unfeasible"
	FeasibilityUnderReview = "under_review"
)

// VoiceRecording stores preserved original voice audio and multilingual transcripts.
type VoiceRecording struct {
	ID                   string    `json:"id"`
	URL                  string    `json:"url,omitempty"`
	MimeType             string    `json:"mime_type"`
	DurationSeconds      float64   `json:"duration_seconds"`
	Language             string    `json:"language"`              // e.g. "si", "ta", "ar", "en", "es", "de"
	AudioData            []byte    `json:"audio_data,omitempty"`  // Raw audio buffer (preserved)
	AudioBase64          string    `json:"audio_base64,omitempty"` // For web audio playback
	OriginalText         string    `json:"original_text"`
	TranslatedEnglish    string    `json:"translated_english"`
	CreatedAt            time.Time `json:"created_at"`
}

// SupportMessage represents a message exchanged in a support or feature request session.
type SupportMessage struct {
	ID                string    `json:"id"`
	TicketID          string    `json:"ticket_id"`
	SenderType        string    `json:"sender_type"` // "user", "ai", "agent", "system"
	SenderName        string    `json:"sender_name"`
	SenderRole        string    `json:"sender_role,omitempty"` // e.g. "customer", "super_admin", "tenant_admin", "ai_concierge"
	Content           string    `json:"content"`
	OriginalLanguage  string    `json:"original_language,omitempty"`
	TranslatedEnglish string    `json:"translated_english,omitempty"`
	AudioURL          string    `json:"audio_url,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// Ticket represents a customer support inquiry, live chat, or voice feature request.
type Ticket struct {
	ID                 string           `json:"id"`
	TenantID           string           `json:"tenant_id,omitempty"`           // Tenant / workspace scope
	PanelID            string           `json:"panel_id,omitempty"`            // Originating panel (e.g. "client", "admin", "super_admin")
	ParentTicketID     string           `json:"parent_ticket_id,omitempty"`     // Hierarchical link (e.g. tenant ticket escalated to super admin)
	UserKey            string           `json:"user_key"`
	UserName           string           `json:"user_name"`
	UserEmail          string           `json:"user_email,omitempty"`
	Subject            string           `json:"subject"`
	Category           string           `json:"category"`                      // "support", "feature_request", "bug"
	Mode               string           `json:"mode"`                          // "ai" or "live"
	Status             string           `json:"status"`                        // "open", "pending", "escalated", "resolved", "closed"
	Priority           string           `json:"priority"`                      // "low", "normal", "high", "urgent"
	Department         string           `json:"department"`
	Language           string           `json:"language"`                      // e.g. "si", "ta", "ar", "en"
	Voice              *VoiceRecording  `json:"voice,omitempty"`              // Preserved voice file & transcripts
	Feasibility        string           `json:"feasibility,omitempty"`         // "simple", "complex", "existing_feature", "under_review"
	ComplexityNotes    string           `json:"complexity_notes,omitempty"`
	Messages           []SupportMessage `json:"messages"`
	EscalatedAt        *time.Time       `json:"escalated_at,omitempty"`
	EscalatedReason    string           `json:"escalated_reason,omitempty"`
	EscalatedBy        string           `json:"escalated_by,omitempty"`
	TargetPanel        string           `json:"target_panel,omitempty"`        // Panel handling escalation (e.g. "super_admin")
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	ResolvedAt         *time.Time       `json:"resolved_at,omitempty"`
}

// TimeAgo returns a human-friendly duration since ticket creation.
func (t *Ticket) TimeAgo() string {
	diff := time.Since(t.CreatedAt)
	if diff < time.Minute {
		return "Just now"
	} else if diff < time.Hour {
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	} else if diff < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
}

// LastMessagePreview returns a snippet of the latest message with sender prefix.
func (t *Ticket) LastMessagePreview() string {
	if len(t.Messages) == 0 {
		return "No messages"
	}
	last := t.Messages[len(t.Messages)-1]
	prefix := ""
	switch last.SenderType {
	case "ai":
		prefix = "🤖 "
	case "agent":
		prefix = "👨‍💼 "
	case "system":
		prefix = "⚙️ "
	default:
		prefix = "👤 "
	}
	txt := last.Content
	if len(txt) > 60 {
		txt = txt[:57] + "..."
	}
	return prefix + txt
}

// TicketFilter parameters for querying tickets.
type TicketFilter struct {
	TenantID   string
	PanelID    string
	Category   string
	Mode       string
	Status     string
	Search     string
	UserKey    string
	Department string
}

// Store defines repository storage for support tickets, chat transcripts, and voice feature requests.
type Store interface {
	CreateTicket(ctx context.Context, t *Ticket) error
	GetTicket(ctx context.Context, id string) (*Ticket, error)
	ListTickets(ctx context.Context, filter TicketFilter) ([]*Ticket, error)
	AddMessage(ctx context.Context, ticketID string, msg *SupportMessage) error
	UpdateTicket(ctx context.Context, t *Ticket) error
	UpdateTicketStatus(ctx context.Context, id string, status string) error
	UpdateTicketMode(ctx context.Context, id string, mode string) error
	EscalateTicket(ctx context.Context, id string, targetPanel string, reason string) error
}

// MemorySupportStore provides an in-memory thread-safe support ticket repository.
type MemorySupportStore struct {
	mu      sync.RWMutex
	tickets map[string]*Ticket
}

var (
	defaultStoreMu sync.RWMutex
	defaultStore   Store
)

// GetDefaultStore returns the globally registered support store.
func GetDefaultStore() Store {
	defaultStoreMu.RLock()
	defer defaultStoreMu.RUnlock()
	if defaultStore == nil {
		defaultStore = NewMemorySupportStore()
	}
	return defaultStore
}

// SetDefaultStore registers the global support store.
func SetDefaultStore(s Store) {
	defaultStoreMu.Lock()
	defer defaultStoreMu.Unlock()
	defaultStore = s
}

// NewMemorySupportStore creates an in-memory store.
func NewMemorySupportStore() *MemorySupportStore {
	return &MemorySupportStore{
		tickets: make(map[string]*Ticket),
	}
}

func (s *MemorySupportStore) CreateTicket(ctx context.Context, t *Ticket) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t.ID == "" {
		t.ID = GenerateTicketID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.UpdatedAt = time.Now()
	s.tickets[t.ID] = t
	return nil
}

func (s *MemorySupportStore) GetTicket(ctx context.Context, id string) (*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, exists := s.tickets[id]
	if !exists {
		return nil, fmt.Errorf("ticket %s not found", id)
	}
	return t, nil
}

func (s *MemorySupportStore) ListTickets(ctx context.Context, filter TicketFilter) ([]*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Ticket, 0, len(s.tickets))
	for _, t := range s.tickets {
		if filter.TenantID != "" && t.TenantID != "" && t.TenantID != filter.TenantID {
			continue
		}
		if filter.PanelID != "" && t.PanelID != "" && t.PanelID != filter.PanelID {
			continue
		}
		if filter.Category != "" && t.Category != filter.Category {
			continue
		}
		if filter.Mode != "" && t.Mode != filter.Mode {
			continue
		}
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		if filter.UserKey != "" && t.UserKey != filter.UserKey {
			continue
		}
		if filter.Department != "" && t.Department != filter.Department {
			continue
		}
		list = append(list, t)
	}
	return list, nil
}

func (s *MemorySupportStore) AddMessage(ctx context.Context, ticketID string, msg *SupportMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, exists := s.tickets[ticketID]
	if !exists {
		return fmt.Errorf("ticket %s not found", ticketID)
	}
	if msg.ID == "" {
		msg.ID = GenerateMessageID()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	t.Messages = append(t.Messages, *msg)
	t.UpdatedAt = time.Now()
	return nil
}

func (s *MemorySupportStore) UpdateTicket(ctx context.Context, t *Ticket) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tickets[t.ID]; !exists {
		return fmt.Errorf("ticket %s not found", t.ID)
	}
	t.UpdatedAt = time.Now()
	s.tickets[t.ID] = t
	return nil
}

func (s *MemorySupportStore) UpdateTicketStatus(ctx context.Context, id string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, exists := s.tickets[id]
	if !exists {
		return fmt.Errorf("ticket %s not found", id)
	}
	t.Status = status
	t.UpdatedAt = time.Now()
	if status == StatusResolved || status == StatusClosed {
		now := time.Now()
		t.ResolvedAt = &now
	}
	return nil
}

func (s *MemorySupportStore) UpdateTicketMode(ctx context.Context, id string, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, exists := s.tickets[id]
	if !exists {
		return fmt.Errorf("ticket %s not found", id)
	}
	t.Mode = mode
	t.UpdatedAt = time.Now()
	return nil
}

func (s *MemorySupportStore) EscalateTicket(ctx context.Context, id string, targetPanel string, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, exists := s.tickets[id]
	if !exists {
		return fmt.Errorf("ticket %s not found", id)
	}
	now := time.Now()
	t.Status = StatusEscalated
	t.Mode = ModeLive
	t.TargetPanel = targetPanel
	t.EscalatedAt = &now
	t.EscalatedReason = reason
	t.UpdatedAt = now
	return nil
}

// GenerateTicketID returns a unique random ticket ID.
func GenerateTicketID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("TKT-%s", hex.EncodeToString(b))
}

// GenerateMessageID returns a unique random message ID.
func GenerateMessageID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("MSG-%s", hex.EncodeToString(b))
}
