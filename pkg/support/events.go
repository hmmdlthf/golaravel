package support

import (
	"time"
)

// TicketCreatedEvent is emitted when a new support or feature request ticket is opened.
type TicketCreatedEvent struct {
	Ticket    *Ticket   `json:"ticket"`
	Timestamp time.Time `json:"timestamp"`
}

// TicketEscalatedEvent is emitted when a ticket transitions from AI mode to live human support.
type TicketEscalatedEvent struct {
	Ticket      *Ticket   `json:"ticket"`
	Reason      string    `json:"reason"`
	TargetPanel string    `json:"target_panel,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// FeatureRequestLoggedEvent is emitted when an AI or voice feature request is processed.
type FeatureRequestLoggedEvent struct {
	Ticket            *Ticket   `json:"ticket"`
	OriginalLanguage  string    `json:"original_language"`
	OriginalText      string    `json:"original_text"`
	TranslatedEnglish string    `json:"translated_english"`
	Feasibility       string    `json:"feasibility"`
	Timestamp         time.Time `json:"timestamp"`
}
