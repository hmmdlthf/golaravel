package support

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	defaultServiceLock     sync.RWMutex
	defaultServiceInstance *Service
)

// GetDefaultService returns the global support concierge service instance.
func GetDefaultService() *Service {
	defaultServiceLock.Lock()
	defer defaultServiceLock.Unlock()
	if defaultServiceInstance == nil {
		defaultServiceInstance = NewService(GetDefaultStore(), GetDefaultKnowledgeBase())
	}
	return defaultServiceInstance
}

// SetDefaultService configures the global support concierge service instance.
func SetDefaultService(svc *Service) {
	defaultServiceLock.Lock()
	defer defaultServiceLock.Unlock()
	defaultServiceInstance = svc
}

// VoiceFeatureRequest encapsulates an inbound audio request in any language.
type VoiceFeatureRequest struct {
	TicketID        string
	AudioBytes      []byte
	MimeType        string
	DurationSeconds float64
	Language        string // e.g. "si", "ta", "ar", "en", "es", "de"
	UserKey         string
	UserName        string
	UserEmail       string
	TenantID        string
	PanelID         string
}

// Service provides high-level support assistance, live agent escalation, and voice feature requests.
type Service struct {
	store           Store
	knowledgeBase   *KnowledgeBase
	speechProcessor SpeechProcessor
	eventDispatcher func(event any)
}

// NewService creates a support service instance.
func NewService(store Store, kb ...*KnowledgeBase) *Service {
	if store == nil {
		store = GetDefaultStore()
	}
	var k *KnowledgeBase
	if len(kb) > 0 && kb[0] != nil {
		k = kb[0]
	} else {
		k = GetDefaultKnowledgeBase()
	}

	return &Service{
		store:           store,
		knowledgeBase:   k,
		speechProcessor: NewDefaultSpeechProcessor(),
	}
}

// SetSpeechProcessor overrides the speech transcription and translation processor.
func (s *Service) SetSpeechProcessor(sp SpeechProcessor) {
	s.speechProcessor = sp
}

// SetEventDispatcher sets the callback to emit framework events or notifications.
func (s *Service) SetEventDispatcher(dispatcher func(event any)) {
	s.eventDispatcher = dispatcher
}

// Store returns the underlying ticket store.
func (s *Service) Store() Store {
	return s.store
}

// KnowledgeBase returns the system feature knowledge base.
func (s *Service) KnowledgeBase() *KnowledgeBase {
	return s.knowledgeBase
}

// SendUserMessage submits a user inquiry. If in AI mode, generates an intelligent answer or escalates to live support.
func (s *Service) SendUserMessage(ctx context.Context, ticketID, userKey, userName, content string, lang ...string) (*SupportMessage, *SupportMessage, error) {
	userLang := "en"
	if len(lang) > 0 && lang[0] != "" {
		userLang = lang[0]
	}

	ticket, err := s.store.GetTicket(ctx, ticketID)
	if err != nil {
		// Auto-create ticket if not exists
		ticket = &Ticket{
			ID:         ticketID,
			UserKey:    userKey,
			UserName:   userName,
			Subject:    deriveSubject(content),
			Category:   CategorySupport,
			Mode:       ModeAI,
			Status:     StatusOpen,
			Priority:   PriorityNormal,
			Department: "Customer Support",
			Language:   userLang,
		}
		if err := s.store.CreateTicket(ctx, ticket); err != nil {
			return nil, nil, fmt.Errorf("failed creating ticket: %w", err)
		}
		s.dispatch(&TicketCreatedEvent{Ticket: ticket})
	}

	userMsg := &SupportMessage{
		TicketID:         ticket.ID,
		SenderType:       "user",
		SenderName:       userName,
		SenderRole:       "customer",
		Content:          content,
		OriginalLanguage: userLang,
		CreatedAt:        time.Now(),
	}

	if err := s.store.AddMessage(ctx, ticket.ID, userMsg); err != nil {
		return nil, nil, fmt.Errorf("failed adding user message: %w", err)
	}

	// 1. Check for escalation triggers (e.g. customer asks for human or expresses unresolved frustration)
	if shouldEscalateToLive(content) {
		_ = s.EscalateToLive(ctx, ticket.ID, "Customer requested live agent assistance or unresolved inquiry")
		escalateMsg := &SupportMessage{
			TicketID:   ticket.ID,
			SenderType: "system",
			SenderName: "Support System",
			SenderRole: "system",
			Content:    formatLocalizedEscalationNotice(userLang),
			CreatedAt:  time.Now(),
		}
		_ = s.store.AddMessage(ctx, ticket.ID, escalateMsg)
		return userMsg, escalateMsg, nil
	}

	// 2. If in ModeLive, do not send bot response; queue for human agent
	if ticket.Mode == ModeLive {
		return userMsg, nil, nil
	}

	// 3. AI Automated Concierge Response
	aiAnswer := s.generateAIResponse(content, userLang)
	aiMsg := &SupportMessage{
		TicketID:         ticket.ID,
		SenderType:       "ai",
		SenderName:       "GoLaravel AI Concierge",
		SenderRole:       "ai_concierge",
		Content:          aiAnswer,
		OriginalLanguage: userLang,
		CreatedAt:        time.Now(),
	}

	if err := s.store.AddMessage(ctx, ticket.ID, aiMsg); err != nil {
		return userMsg, nil, fmt.Errorf("failed storing AI message: %w", err)
	}

	return userMsg, aiMsg, nil
}

// SubmitVoiceFeatureRequest processes an inbound voice recording in any language, evaluates it against the knowledge base,
// creates a feature request ticket, preserves the original audio file, and replies in the customer's language.
func (s *Service) SubmitVoiceFeatureRequest(ctx context.Context, req VoiceFeatureRequest) (*Ticket, *SupportMessage, error) {
	lang := req.Language
	if lang == "" || !isValidLanguage(lang) {
		lang = "en"
	}

	// 1. Process voice audio: transcribe original language & translate into English
	originalTranscript, englishTranslation, detectedLang, err := s.speechProcessor.ProcessVoice(ctx, req.AudioBytes, req.MimeType, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("speech processing failed: %w", err)
	}
	if detectedLang != "" {
		lang = detectedLang
	}

	// 2. Preserve original audio recording
	recordingID := fmt.Sprintf("REC-%s", GenerateTicketID())
	voiceRec := &VoiceRecording{
		ID:                recordingID,
		MimeType:          req.MimeType,
		DurationSeconds:   req.DurationSeconds,
		Language:          lang,
		AudioData:         req.AudioBytes,
		AudioBase64:       base64.StdEncoding.EncodeToString(req.AudioBytes),
		OriginalText:      originalTranscript,
		TranslatedEnglish: englishTranslation,
		CreatedAt:         time.Now(),
	}

	// 3. Knowledge Base Evaluation
	matchedFeature := s.knowledgeBase.FindMatching(englishTranslation)

	var feasibility string
	var aiReplyContent string

	if matchedFeature != nil && matchedFeature.Status == "active" {
		// Case 1: Feature already exists in system!
		feasibility = FeasibilityExisting
		aiReplyContent = formatFeatureAlreadyExistsReply(lang, matchedFeature)
	} else {
		// Evaluate complexity
		isComplex := evaluateRequestComplexity(englishTranslation)
		if isComplex {
			// Case 2: Complex / requires review
			feasibility = FeasibilityComplex
			aiReplyContent = formatComplexFeatureReviewReply(lang, englishTranslation)
		} else {
			// Case 3: Simple / planned
			feasibility = FeasibilitySimple
			aiReplyContent = formatSimpleFeatureAcceptedReply(lang, englishTranslation)
		}
	}

	// 4. Create Feature Request Ticket
	subject := "Feature Request: " + summarizeText(englishTranslation, 45)
	ticketID := req.TicketID
	if ticketID == "" {
		ticketID = GenerateTicketID()
	}
	ticket := &Ticket{
		ID:              ticketID,
		TenantID:        req.TenantID,
		PanelID:         req.PanelID,
		UserKey:         req.UserKey,
		UserName:        req.UserName,
		UserEmail:       req.UserEmail,
		Subject:         subject,
		Category:        CategoryFeatureRequest,
		Mode:            ModeAI,
		Status:          StatusOpen,
		Priority:        PriorityNormal,
		Department:      "Product & Engineering",
		Language:        lang,
		Voice:           voiceRec,
		Feasibility:     feasibility,
		ComplexityNotes: fmt.Sprintf("Evaluated feasibility: %s. English transcript: %s", feasibility, englishTranslation),
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// 5. Initial messages
	userMsg := SupportMessage{
		ID:                GenerateMessageID(),
		TicketID:          ticket.ID,
		SenderType:        "user",
		SenderName:        req.UserName,
		SenderRole:        "customer",
		Content:           originalTranscript,
		OriginalLanguage:  lang,
		TranslatedEnglish: englishTranslation,
		CreatedAt:         time.Now(),
	}

	aiMsg := SupportMessage{
		ID:                GenerateMessageID(),
		TicketID:          ticket.ID,
		SenderType:        "ai",
		SenderName:        "GoLaravel AI Concierge",
		SenderRole:        "ai_concierge",
		Content:           aiReplyContent,
		OriginalLanguage:  lang,
		TranslatedEnglish: aiReplyContent,
		CreatedAt:         time.Now().Add(time.Second),
	}

	ticket.Messages = []SupportMessage{userMsg, aiMsg}

	if err := s.store.CreateTicket(ctx, ticket); err != nil {
		return nil, nil, fmt.Errorf("failed creating feature request ticket: %w", err)
	}

	// 6. Dispatch event alert for administrators
	s.dispatch(&FeatureRequestLoggedEvent{
		Ticket:            ticket,
		OriginalLanguage:  lang,
		OriginalText:      originalTranscript,
		TranslatedEnglish: englishTranslation,
		Feasibility:       feasibility,
	})

	return ticket, &aiMsg, nil
}

// EscalateToLive transitions a ticket from AI bot mode to human live chat agent.
func (s *Service) EscalateToLive(ctx context.Context, ticketID, reason string) error {
	ticket, err := s.store.GetTicket(ctx, ticketID)
	if err != nil {
		return err
	}

	now := time.Now()
	ticket.Mode = ModeLive
	ticket.Status = StatusEscalated
	ticket.EscalatedAt = &now
	ticket.EscalatedReason = reason
	ticket.UpdatedAt = now

	if err := s.store.UpdateTicket(ctx, ticket); err != nil {
		return err
	}

	s.dispatch(&TicketEscalatedEvent{
		Ticket:      ticket,
		Reason:      reason,
		TargetPanel: ticket.TargetPanel,
	})

	return nil
}

// EscalateToSuperAdmin escalates a ticket from a tenant/child panel to the Super Admin panel.
func (s *Service) EscalateToSuperAdmin(ctx context.Context, ticketID, superAdminPanelID, reason string) error {
	ticket, err := s.store.GetTicket(ctx, ticketID)
	if err != nil {
		return err
	}

	now := time.Now()
	ticket.Mode = ModeLive
	ticket.Status = StatusEscalated
	ticket.TargetPanel = superAdminPanelID
	ticket.EscalatedAt = &now
	ticket.EscalatedReason = reason
	ticket.UpdatedAt = now

	sysMsg := &SupportMessage{
		TicketID:   ticket.ID,
		SenderType: "system",
		SenderName: "Panel Orchestrator",
		SenderRole: "system",
		Content:    fmt.Sprintf("Ticket escalated to Super Admin panel (%s): %s", superAdminPanelID, reason),
		CreatedAt:  now,
	}
	_ = s.store.AddMessage(ctx, ticket.ID, sysMsg)

	if err := s.store.UpdateTicket(ctx, ticket); err != nil {
		return err
	}

	s.dispatch(&TicketEscalatedEvent{
		Ticket:      ticket,
		Reason:      reason,
		TargetPanel: superAdminPanelID,
	})

	return nil
}

// SendAgentMessage records a live human operator response.
func (s *Service) SendAgentMessage(ctx context.Context, ticketID, agentID, agentName, agentRole, content string) (*SupportMessage, error) {
	ticket, err := s.store.GetTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}

	if agentRole == "" {
		agentRole = "support_agent"
	}

	msg := &SupportMessage{
		TicketID:   ticket.ID,
		SenderType: "agent",
		SenderName: agentName,
		SenderRole: agentRole,
		Content:    content,
		CreatedAt:  time.Now(),
	}

	if err := s.store.AddMessage(ctx, ticket.ID, msg); err != nil {
		return nil, err
	}

	// Update status to pending customer reply
	_ = s.store.UpdateTicketStatus(ctx, ticket.ID, StatusPending)

	return msg, nil
}

// ResolveTicket marks a ticket as resolved with optional resolution notes.
func (s *Service) ResolveTicket(ctx context.Context, ticketID, notes string) error {
	now := time.Now()
	ticket, err := s.store.GetTicket(ctx, ticketID)
	if err != nil {
		return err
	}

	ticket.Status = StatusResolved
	ticket.ResolvedAt = &now
	ticket.UpdatedAt = now

	if notes != "" {
		msg := &SupportMessage{
			TicketID:   ticket.ID,
			SenderType: "system",
			SenderName: "Resolution",
			SenderRole: "system",
			Content:    "Ticket marked as resolved: " + notes,
			CreatedAt:  now,
		}
		_ = s.store.AddMessage(ctx, ticket.ID, msg)
	}

	return s.store.UpdateTicket(ctx, ticket)
}

func (s *Service) dispatch(event any) {
	if s.eventDispatcher != nil {
		s.eventDispatcher(event)
	}
}

// shouldEscalateToLive detects escalation phrases in content.
func shouldEscalateToLive(content string) bool {
	c := strings.ToLower(content)
	triggers := []string{
		"speak to human", "live agent", "not working", "talk to support",
		"talk to someone", "escalate", "customer care", "agent please",
		"call me", "human please", "operator", "speak with agent",
		"unresolved", "doesn't help", "human support", "human representative",
		"human agent", "talk to human", "real person", "person please",
		"connect me to human", "speak with human",
	}
	for _, t := range triggers {
		if strings.Contains(c, t) {
			return true
		}
	}
	return false
}

// evaluateRequestComplexity analyzes whether a feature request is simple or architecturally complex.
func evaluateRequestComplexity(englishText string) bool {
	txt := strings.ToLower(englishText)
	complexKeywords := []string{
		"ai model", "custom neural", "blockchain", "multi-region replication",
		"distributed lock", "high availability", "zero downtime cluster",
		"offline sync conflict", "kernel module", "p2p mesh", "video transcoder",
	}
	for _, kw := range complexKeywords {
		if strings.Contains(txt, kw) {
			return true
		}
	}
	return false
}

func (s *Service) generateAIResponse(question, lang string) string {
	feat := s.knowledgeBase.FindMatching(question)
	if feat != nil {
		return formatFeatureAlreadyExistsReply(lang, feat)
	}

	switch lang {
	case "si":
		return "ඔබගේ පණිවිඩයට ස්තූතියි. අපගේ පද්ධතිය ඔබගේ ගැටලුව පරීක්ෂා කරමින් පවතී. ඔබට සජීවී නියෝජිතයෙකු සමඟ කතා කිරීමට අවශ්‍ය නම් 'Live Agent' ලෙස සටහන් කරන්න."
	case "ta":
		return "உங்கள் செய்திக்கு நன்றி. எங்கள் அமைப்பு உங்கள் கோரிக்கையைச் சரிபார்க்கிறது. நேரடி முகவருடன் பேச விரும்பினால் 'Live Agent' என்று தட்டச்சு செய்யவும்."
	case "ar":
		return "شكراً لتواصلك معنا. يقوم نظام الذكاء الاصطناعي بمراجعة استفسارك. إذا كنت ترغب في التحدث إلى وكيل مباشر، يرجى كتابة 'وكيل مباشر' وسنقوم بتحويلك فوراً."
	case "es":
		return "Gracias por contactarnos. Nuestro asistente virtual está revisando su consulta. Si desea hablar con un agente humano, escriba 'agente en vivo' para ser transferido."
	case "de":
		return "Vielen Dank für Ihre Nachricht. Unser System prüft Ihre Anfrage. Wenn Sie mit einem Mitarbeiter sprechen möchten, schreiben Sie bitte 'Live-Agent'."
	default:
		return "Thank you for contacting support! Our AI Concierge is processing your inquiry. If you'd like to speak with a human support agent at any time, just type 'live agent' and we will escalate immediately."
	}
}

func formatLocalizedEscalationNotice(lang string) string {
	switch lang {
	case "si":
		return "⚠️ සංවාදය සජීවී සහායක නියෝජිතයෙකු වෙත යොමු කරන ලදී. නියෝජිතයෙකු ඉක්මනින් සම්බන්ධ වනු ඇත."
	case "ta":
		return "⚠️ உங்கள் உரையாடல் நேரடி ஆதரவு முகவருக்கு மாற்றப்பட்டுள்ளது. விரைவில் ஒரு முகவர் இணைவார்."
	case "ar":
		return "⚠️ تم تحويل المحادثة إلى الدعم المباشر. سينضم أحد ممثلي الدعم الفني قريباً."
	case "es":
		return "⚠️ La conversación ha sido transferida a un agente de soporte en vivo. Un representante se unirá en breve."
	case "de":
		return "⚠️ Das Gespräch wurde an einen Live-Support-Mitarbeiter weitergeleitet. Ein Agent wird sich in Kürze melden."
	default:
		return "⚠️ Conversation escalated to Live Human Support. An agent will join shortly."
	}
}

func formatFeatureAlreadyExistsReply(lang string, feat *FeatureKnowledge) string {
	switch lang {
	case "si":
		return fmt.Sprintf("විශේෂ ආරංචියක්! මෙම විශේෂාංගය ('%s') අපගේ පද්ධතියේ දැනටමත් ක්‍රියාත්මකයි (%s හි නිකුත් කර ඇත). වැඩි විස්තර සඳහා: %s", feat.Title, feat.VersionReleased, feat.Description)
	case "ta":
		return fmt.Sprintf("நல்ல செய்தி! இந்த அம்சம் ('%s') ஏற்கனவே எங்கள் கணினியில் கிடைக்கிறது (%s இல் வெளியிடப்பட்டது). விவரங்கள்: %s", feat.Title, feat.VersionReleased, feat.Description)
	case "ar":
		return fmt.Sprintf("خبر رائع! هذه الميزة ('%s') متوفرة بالفعل في نظامنا (أُطلقت في %s). التفاصيل: %s", feat.Title, feat.VersionReleased, feat.Description)
	case "es":
		return fmt.Sprintf("¡Buenas noticias! La función '%s' ya está disponible en nuestro sistema (lanzada en %s). Detalles: %s", feat.Title, feat.VersionReleased, feat.Description)
	case "de":
		return fmt.Sprintf("Gute Neuigkeiten! Das Feature '%s' ist bereits in unserem System verfügbar (veröffentlicht in %s). Details: %s", feat.Title, feat.VersionReleased, feat.Description)
	default:
		return fmt.Sprintf("Great news! The feature '%s' is already available in our system (released in %s). %s", feat.Title, feat.VersionReleased, feat.Description)
	}
}

func formatSimpleFeatureAcceptedReply(lang, englishText string) string {
	switch lang {
	case "si":
		return "ඔබගේ වටිනා යෝජනාවට ස්තූතියි! අපි මෙම විශේෂාංග ඉල්ලීම අපගේ සංවර්ධන සැලැස්මට ඇතුළත් කළ අතර කණ්ඩායම එය එක් කිරීමට කටයුතු කරනු ඇත."
	case "ta":
		return "உங்கள் மதிப்புமிக்க அம்சக் கோரிக்கைக்கு நன்றி! இதை எங்கள் மேம்பாட்டுப் பட்டியலில் சேர்த்துள்ளோம், விரைவில் இதைச் சேர்க்க எங்கள் குழு செயல்படும்."
	case "ar":
		return "شكراً لاقتراحك القيم! لقد سجلنا طلب هذه الميزة في خطة التطوير القادمة وسيعمل فريقنا على برمجتها قريباً."
	case "es":
		return "¡Gracias por su valiosa sugerencia! Hemos registrado esta solicitud de función en nuestra lista de desarrollo y nuestro equipo trabajará para implementarla."
	case "de":
		return "Vielen Dank für Ihren wertvollen Vorschlag! Wir haben diesen Feature-Wunsch in unserer Entwicklungs-Roadmap aufgenommen."
	default:
		return "Thank you for your valuable suggestion! We have submitted this feature request to our development roadmap and our team will work on adding it."
	}
}

func formatComplexFeatureReviewReply(lang, englishText string) string {
	switch lang {
	case "si":
		return "ඔබගේ යෝජනාවට ස්තූතියි! මෙම විශේෂාංගය තාක්ෂණික වශයෙන් සංකීර්ණ බැවින්, අපගේ ඉංජිනේරු කණ්ඩායම එහි හැකියාව සලකා බලා ඉදිරියේදී ඔබට දැනුම් දෙනු ඇත."
	case "ta":
		return "உங்கள் யோசனைக்கு நன்றி! இந்த அம்சம் சிக்கலானதாக இருப்பதால், எங்கள் பொறியியல் குழு இதை மதிப்பாய்வு செய்து முடிவை உங்களுக்கு அறிவிக்கும்."
	case "ar":
		return "شكراً لاقتراحك! نظراً لأن هذا الطلب يتطلب دراسة معمارية وهندسية متقدمة، سيقوم فريق التطوير بمناقشة إمكانية تنفيذه وسنوافيك بالرد."
	case "es":
		return "Gracias por su propuesta. Dado que esta función involucra consideraciones arquitectónicas complejas, nuestro equipo técnico evaluará su viabilidad y le informaremos."
	case "de":
		return "Vielen Dank für Ihren Vorschlag. Da dieses Feature komplexe architektonische Anforderungen erfordert, wird unser Entwicklerteam die Machbarkeit prüfen und Sie auf dem Laufenden halten."
	default:
		return "Thank you for your feature suggestion. Because this involves complex architectural considerations, our engineering team will discuss and review its feasibility, and we will update you on our progress."
	}
}

func deriveSubject(content string) string {
	c := strings.TrimSpace(content)
	if len(c) > 50 {
		return c[:47] + "..."
	}
	if c == "" {
		return "Support Inquiry"
	}
	return c
}

func summarizeText(txt string, maxLen int) string {
	t := strings.TrimSpace(txt)
	if len(t) <= maxLen {
		return t
	}
	return t[:maxLen-3] + "..."
}
