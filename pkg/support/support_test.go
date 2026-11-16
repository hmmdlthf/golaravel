package support

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSupport_AIChatAndEscalation(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySupportStore()
	service := NewService(store)

	var dispatchedEvent any
	service.SetEventDispatcher(func(event any) {
		dispatchedEvent = event
	})

	// 1. Initial general inquiry
	userMsg, aiReply, err := service.SendUserMessage(ctx, "TKT-100", "user_1", "Kasun Perera", "Hello, how does the system work?", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userMsg == nil || aiReply == nil {
		t.Fatalf("expected both userMsg and aiReply")
	}
	if aiReply.SenderType != "ai" {
		t.Errorf("expected sender ai, got %s", aiReply.SenderType)
	}

	ticket, err := store.GetTicket(ctx, "TKT-100")
	if err != nil {
		t.Fatalf("ticket not found: %v", err)
	}
	if ticket.Mode != ModeAI || ticket.Status != StatusOpen {
		t.Errorf("expected ModeAI and StatusOpen, got mode=%s status=%s", ticket.Mode, ticket.Status)
	}

	// 2. User gets stuck or asks to speak to live agent
	userMsg2, sysEscalate, err := service.SendUserMessage(ctx, "TKT-100", "user_1", "Kasun Perera", "This didn't help. I need to speak to human support agent please!", "en")
	if err != nil {
		t.Fatalf("unexpected error on escalation: %v", err)
	}
	if userMsg2 == nil || sysEscalate == nil {
		t.Fatalf("expected user message and escalation notice")
	}
	if sysEscalate.SenderType != "system" {
		t.Errorf("expected system sender for escalation notice")
	}

	// Check ticket escalated state
	ticketAfter, _ := store.GetTicket(ctx, "TKT-100")
	if ticketAfter.Mode != ModeLive || ticketAfter.Status != StatusEscalated {
		t.Errorf("expected ModeLive and StatusEscalated, got mode=%s status=%s", ticketAfter.Mode, ticketAfter.Status)
	}
	if ticketAfter.EscalatedAt == nil {
		t.Errorf("expected EscalatedAt timestamp to be set")
	}

	// Verify event was dispatched
	if _, ok := dispatchedEvent.(*TicketEscalatedEvent); !ok {
		t.Errorf("expected TicketEscalatedEvent to be dispatched")
	}

	// 3. Human Agent joins and replies
	agentMsg, err := service.SendAgentMessage(ctx, "TKT-100", "agent_42", "Sarah Connor", "support_agent", "Hello Kasun, I am taking over this ticket. Let me resolve this for you.")
	if err != nil {
		t.Fatalf("failed sending agent message: %v", err)
	}
	if agentMsg.SenderRole != "support_agent" {
		t.Errorf("expected agent role support_agent")
	}

	// 4. Resolve Ticket
	if err := service.ResolveTicket(ctx, "TKT-100", "Issue resolved via agent guidance"); err != nil {
		t.Fatalf("failed to resolve ticket: %v", err)
	}
	resolvedTicket, _ := store.GetTicket(ctx, "TKT-100")
	if resolvedTicket.Status != StatusResolved || resolvedTicket.ResolvedAt == nil {
		t.Errorf("expected StatusResolved and ResolvedAt set")
	}
}

func TestSupport_VoiceFeatureRequest_MultilingualAndKnowledge(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySupportStore()
	service := NewService(store)

	var lastEvent any
	service.SetEventDispatcher(func(e any) {
		lastEvent = e
	})

	// 1. Sinhala Voice Request for a simple new feature
	sinhalaReq := VoiceFeatureRequest{
		AudioBytes:      []byte("fake_sinhala_audio_bytes_data"),
		MimeType:        "audio/webm",
		DurationSeconds: 6.4,
		Language:        "si",
		UserKey:         "user_si_1",
		UserName:        "Nimal Bandara",
		UserEmail:       "nimal@colombo.lk",
		TenantID:        "tenant_colombo",
		PanelID:         "client",
	}

	ticketSI, aiReplySI, err := service.SubmitVoiceFeatureRequest(ctx, sinhalaReq)
	if err != nil {
		t.Fatalf("failed submitting sinhala voice request: %v", err)
	}
	if ticketSI.Category != CategoryFeatureRequest {
		t.Errorf("expected CategoryFeatureRequest, got %s", ticketSI.Category)
	}
	if ticketSI.Voice == nil || len(ticketSI.Voice.AudioData) == 0 {
		t.Errorf("expected voice recording and raw audio bytes to be preserved")
	}
	if ticketSI.Voice.Language != "si" {
		t.Errorf("expected voice language 'si', got %s", ticketSI.Voice.Language)
	}
	if ticketSI.Feasibility != FeasibilitySimple {
		t.Errorf("expected FeasibilitySimple for whatsapp notification request, got %s", ticketSI.Feasibility)
	}
	if !strings.Contains(aiReplySI.Content, "ස්තූතියි") {
		t.Errorf("expected Sinhala reply acknowledging feature request, got: %s", aiReplySI.Content)
	}

	// Check event
	if ev, ok := lastEvent.(*FeatureRequestLoggedEvent); !ok || ev.OriginalLanguage != "si" {
		t.Errorf("expected FeatureRequestLoggedEvent with lang si")
	}

	// 2. Arabic Voice Request for an ALREADY EXISTING feature (Soft Deletes / Recycle bin)
	mockSP := NewDefaultSpeechProcessor()
	mockSP.SetCustomHandler(func(ctx context.Context, audioBytes []byte, mimeType string, hintLang string) (string, string, string, error) {
		return "نحتاج إلى سلة محذوفات لاستعادة السجلات المحذوفة", "We need a recycle bin and soft delete to restore deleted records", "ar", nil
	})
	service.SetSpeechProcessor(mockSP)

	arabicReq := VoiceFeatureRequest{
		AudioBytes:      []byte("fake_arabic_audio_data"),
		MimeType:        "audio/wav",
		DurationSeconds: 4.8,
		Language:        "ar",
		UserKey:         "user_ar_1",
		UserName:        "Tariq Al-Mansoor",
		UserEmail:       "tariq@riyadh.sa",
		TenantID:        "tenant_riyadh",
		PanelID:         "admin",
	}

	ticketAR, aiReplyAR, err := service.SubmitVoiceFeatureRequest(ctx, arabicReq)
	if err != nil {
		t.Fatalf("failed submitting arabic voice request: %v", err)
	}
	if ticketAR.Feasibility != FeasibilityExisting {
		t.Errorf("expected FeasibilityExisting since soft deletes already exist in system, got %s", ticketAR.Feasibility)
	}
	if !strings.Contains(aiReplyAR.Content, "متوفرة بالفعل") {
		t.Errorf("expected Arabic reply informing feature already exists, got: %s", aiReplyAR.Content)
	}

	// 3. Complex Feature Request (e.g. Distributed P2P Mesh with Custom Neural Model)
	mockSP.SetCustomHandler(func(ctx context.Context, audioBytes []byte, mimeType string, hintLang string) (string, string, string, error) {
		return "We need offline P2P mesh replication and custom neural network sync", "We need offline P2P mesh replication and custom neural network sync", "en", nil
	})

	complexReq := VoiceFeatureRequest{
		AudioBytes:      []byte("fake_complex_audio"),
		MimeType:        "audio/ogg",
		DurationSeconds: 8.2,
		Language:        "en",
		UserKey:         "user_eng_1",
		UserName:        "Dr. Robert Ford",
		UserEmail:       "ford@delos.com",
		TenantID:        "tenant_global",
		PanelID:         "admin",
	}

	ticketComplex, aiReplyComplex, err := service.SubmitVoiceFeatureRequest(ctx, complexReq)
	if err != nil {
		t.Fatalf("failed submitting complex voice request: %v", err)
	}
	if ticketComplex.Feasibility != FeasibilityComplex {
		t.Errorf("expected FeasibilityComplex, got %s", ticketComplex.Feasibility)
	}
	if !strings.Contains(aiReplyComplex.Content, "engineering team will discuss and review") {
		t.Errorf("expected review message for complex request, got: %s", aiReplyComplex.Content)
	}
}

func TestSupport_LanguageResolutionAndCookies(t *testing.T) {
	// 1. Default to English
	req1 := httptest.NewRequest("GET", "/support/chat", nil)
	if lang := ResolveSupportLanguage(req1); lang != "en" {
		t.Errorf("expected default en, got %s", lang)
	}

	// 2. Cookie resolution
	req2 := httptest.NewRequest("GET", "/support/chat", nil)
	req2.AddCookie(&http.Cookie{Name: LanguageCookieName, Value: "si"})
	if lang := ResolveSupportLanguage(req2); lang != "si" {
		t.Errorf("expected si from cookie, got %s", lang)
	}

	// 3. Query param overrides cookie
	req3 := httptest.NewRequest("GET", "/support/chat?lang=ta", nil)
	req3.AddCookie(&http.Cookie{Name: LanguageCookieName, Value: "si"})
	if lang := ResolveSupportLanguage(req3); lang != "ta" {
		t.Errorf("expected ta from query param, got %s", lang)
	}

	// 4. Set cookie helper
	w := httptest.NewRecorder()
	SetSupportLanguageCookie(w, "ar")
	cookies := w.Result().Cookies()
	var found *http.Cookie
	for _, c := range cookies {
		if c.Name == LanguageCookieName {
			found = c
			break
		}
	}
	if found == nil || found.Value != "ar" {
		t.Errorf("expected cookie to be set with value 'ar'")
	}
}

func TestSupport_HierarchicalEscalation(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySupportStore()
	service := NewService(store)

	// Create initial ticket on tenant panel
	ticket := &Ticket{
		ID:       "TKT-TENANT-01",
		TenantID: "tenant_alpha",
		PanelID:  "tenant_admin",
		UserKey:  "user_5",
		UserName: "Alice Cooper",
		Subject:  "Server database latency spike",
		Category: CategorySupport,
		Mode:     ModeLive,
		Status:   StatusOpen,
	}
	_ = store.CreateTicket(ctx, ticket)

	// Tenant admin escalates ticket upstream to Super Admin Panel
	err := service.EscalateToSuperAdmin(ctx, ticket.ID, "super_admin", "Requires root infrastructure diagnostics")
	if err != nil {
		t.Fatalf("unexpected error on super admin escalation: %v", err)
	}

	updated, _ := store.GetTicket(ctx, ticket.ID)
	if updated.TargetPanel != "super_admin" {
		t.Errorf("expected TargetPanel super_admin, got %s", updated.TargetPanel)
	}
	if updated.Status != StatusEscalated {
		t.Errorf("expected StatusEscalated, got %s", updated.Status)
	}

	// Verify system message was appended
	if len(updated.Messages) == 0 || !strings.Contains(updated.Messages[0].Content, "super_admin") {
		t.Errorf("expected escalation message in ticket transcript")
	}
}

func TestTicketIDsAreUnguessable(t *testing.T) {
	id := GenerateTicketID()
	if len(id) != len("TKT-")+32 {
		t.Fatalf("ticket IDs must carry 128 bits: %q", id)
	}
	if GenerateTicketID() == id {
		t.Fatal("ticket IDs repeat")
	}
}
