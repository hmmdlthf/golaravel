package support

import (
	"strings"
	"sync"
)

// FeatureKnowledge describes a system feature or capability known to the AI.
type FeatureKnowledge struct {
	Key             string   `json:"key"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	VersionReleased string   `json:"version_released"`
	Status          string   `json:"status"` // "active", "beta", "planned", "deprecated"
	DocsURL         string   `json:"docs_url,omitempty"`
	Keywords        []string `json:"keywords"`
	Complexity      string   `json:"complexity,omitempty"` // "simple", "moderate", "complex"
}

// KnowledgeBase manages the registry of system features up to the latest release.
type KnowledgeBase struct {
	mu       sync.RWMutex
	features map[string]FeatureKnowledge
}

var (
	defaultKBMu sync.RWMutex
	defaultKB   *KnowledgeBase
)

// GetDefaultKnowledgeBase returns the system-wide feature knowledge base.
func GetDefaultKnowledgeBase() *KnowledgeBase {
	defaultKBMu.RLock()
	defer defaultKBMu.RUnlock()
	if defaultKB == nil {
		defaultKB = NewKnowledgeBase()
	}
	return defaultKB
}

// SetDefaultKnowledgeBase sets the system-wide feature knowledge base.
func SetDefaultKnowledgeBase(kb *KnowledgeBase) {
	defaultKBMu.Lock()
	defer defaultKBMu.Unlock()
	defaultKB = kb
}

// NewKnowledgeBase initializes a knowledge base pre-seeded with framework capabilities up to v1.25.0.
func NewKnowledgeBase() *KnowledgeBase {
	kb := &KnowledgeBase{
		features: make(map[string]FeatureKnowledge),
	}
	kb.seedSystemKnowledge()
	return kb
}

// Register adds or updates a feature in the knowledge base.
func (kb *KnowledgeBase) Register(feat FeatureKnowledge) {
	kb.mu.Lock()
	defer kb.mu.Unlock()
	kb.features[feat.Key] = feat
}

// RegisterBatch registers multiple features at once.
func (kb *KnowledgeBase) RegisterBatch(feats []FeatureKnowledge) {
	kb.mu.Lock()
	defer kb.mu.Unlock()
	for _, f := range feats {
		kb.features[f.Key] = f
	}
}

// All returns all known features.
func (kb *KnowledgeBase) All() []FeatureKnowledge {
	kb.mu.RLock()
	defer kb.mu.RUnlock()

	list := make([]FeatureKnowledge, 0, len(kb.features))
	for _, f := range kb.features {
		list = append(list, f)
	}
	return list
}

// FindMatching checks if a user query corresponds to an already existing or known feature.
func (kb *KnowledgeBase) FindMatching(query string) *FeatureKnowledge {
	kb.mu.RLock()
	defer kb.mu.RUnlock()

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}

	// 1. Direct title or key match
	for _, f := range kb.features {
		if strings.Contains(q, strings.ToLower(f.Title)) || strings.Contains(q, strings.ToLower(f.Key)) {
			return &f
		}
	}

	// 2. Keyword matching
	for _, f := range kb.features {
		matchCount := 0
		for _, kw := range f.Keywords {
			if strings.Contains(q, strings.ToLower(kw)) {
				matchCount++
			}
		}
		if matchCount >= 2 || (len(f.Keywords) == 1 && matchCount == 1) {
			return &f
		}
	}

	return nil
}

// seedSystemKnowledge populates capabilities up to latest release.
func (kb *KnowledgeBase) seedSystemKnowledge() {
	feats := []FeatureKnowledge{
		{
			Key:             "soft_deletes",
			Title:           "Soft Deletes & Trash Recovery Bin",
			Description:     "Allows soft-deleting records, recovering them from the trash bin, and permanently force-deleting with confirmation.",
			VersionReleased: "v1.18.0",
			Status:          "active",
			DocsURL:         "/admin/docs/soft-deletes",
			Keywords:        []string{"soft delete", "trash", "restore", "recycle bin", "recover deleted"},
			Complexity:      "simple",
		},
		{
			Key:             "audit_trail",
			Title:           "Activity Log & Audit Trail Timeline",
			Description:     "Automatically logs record create, update, delete, and restore actions with before/after changeset diffs and interactive timeline.",
			VersionReleased: "v1.17.0",
			Status:          "active",
			DocsURL:         "/admin/docs/audit-trail",
			Keywords:        []string{"audit", "activity log", "history", "changeset", "diff", "timeline", "who changed"},
			Complexity:      "simple",
		},
		{
			Key:             "rtl_i18n",
			Title:           "Multi-Language & RTL Layout Support",
			Description:     "Full internationalization with Arabic, Spanish, German, and English translations, and complete Right-to-Left (RTL) layout flipping.",
			VersionReleased: "v1.19.0",
			Status:          "active",
			DocsURL:         "/admin/docs/i18n",
			Keywords:        []string{"arabic", "rtl", "right to left", "language", "translation", "spanish", "german", "sinhala", "tamil"},
			Complexity:      "simple",
		},
		{
			Key:             "customizable_bottom_nav",
			Title:           "Customizable Mobile Bottom Navigation",
			Description:     "Customizable bottom navigation slots, labels toggle, and circular FAB modal trigger for pagination on mobile devices.",
			VersionReleased: "v1.21.0",
			Status:          "active",
			DocsURL:         "/admin/docs/mobile-nav",
			Keywords:        []string{"bottom bar", "bottom nav", "mobile navigation", "fab pagination", "mobile icons"},
			Complexity:      "simple",
		},
		{
			Key:             "column_config",
			Title:           "Table Column Selectability & Custom Defaults",
			Description:     "End-user column toggling, column resizing, drag-and-drop row reordering, and configurable default columns.",
			VersionReleased: "v1.22.0",
			Status:          "active",
			DocsURL:         "/admin/docs/table-columns",
			Keywords:        []string{"column", "hide column", "show column", "resize column", "reorder rows", "table columns"},
			Complexity:      "simple",
		},
		{
			Key:             "version_update_detection",
			Title:           "Panel Versioning & Hot Update Reload",
			Description:     "Detects new server deployments, notifies active users with a non-intrusive update banner, and reloads without loss of context.",
			VersionReleased: "v1.24.0",
			Status:          "active",
			DocsURL:         "/admin/docs/appupdate",
			Keywords:        []string{"version", "update", "hot reload", "new release", "banner", "build id"},
			Complexity:      "simple",
		},
		{
			Key:             "ai_support_concierge",
			Title:           "AI Support Concierge & Live Chat",
			Description:     "Integrated AI first-response support concierge with automatic escalation to human live chat agents, and multimodal voice feature requests.",
			VersionReleased: "v1.25.0",
			Status:          "active",
			DocsURL:         "/admin/docs/support",
			Keywords:        []string{"chat", "support", "ai support", "live chat", "ticket", "concierge", "voice request"},
			Complexity:      "moderate",
		},
		{
			Key:             "multi_tenancy",
			Title:           "Multi-Tenancy & Tenant Privacy Scoping",
			Description:     "Tenant context resolution, database privacy scoping, tenant-specific navigation, and tenant switcher dropdowns.",
			VersionReleased: "v1.12.0",
			Status:          "active",
			DocsURL:         "/admin/docs/tenancy",
			Keywords:        []string{"tenant", "multi tenancy", "workspace", "organization", "isolation"},
			Complexity:      "moderate",
		},
		{
			Key:             "form_wizards",
			Title:           "Multi-Step Form Wizards",
			Description:     "Step-by-step progress flows with dynamic validation autofocus, wizard navigation buttons, and step descriptions.",
			VersionReleased: "v1.16.0",
			Status:          "active",
			DocsURL:         "/admin/docs/wizards",
			Keywords:        []string{"wizard", "multi step", "steps", "form step", "progress"},
			Complexity:      "simple",
		},
		{
			Key:             "export_import_csv",
			Title:           "Streaming CSV Data Export & Import",
			Description:     "Memory-efficient streaming CSV export and batch CSV import with automatic schema mapping.",
			VersionReleased: "v1.10.0",
			Status:          "active",
			DocsURL:         "/admin/docs/export-import",
			Keywords:        []string{"export", "import", "csv", "download", "upload data"},
			Complexity:      "simple",
		},
	}

	for _, f := range feats {
		kb.features[f.Key] = f
	}
}
