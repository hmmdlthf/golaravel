package flutter

import (
	"fmt"
	"strings"
)

// LoadingStyle defines the visual style for the pre-Flutter HTML/CSS loading screen.
type LoadingStyle string

const (
	// LoadingStyleSpinner shows a modern circular gradient ring spinner with subtle pulse.
	LoadingStyleSpinner LoadingStyle = "spinner"

	// LoadingStylePulse shows an app icon / logo with a smooth glowing pulse effect.
	LoadingStylePulse LoadingStyle = "pulse"

	// LoadingStyleBar shows a sleek top indeterminate progress / shimmer bar with brand title.
	LoadingStyleBar LoadingStyle = "bar"

	// LoadingStyleDots shows 3 bouncing dots with brand caption.
	LoadingStyleDots LoadingStyle = "dots"

	// LoadingStyleMinimal shows a subtle, unobtrusive centered spinner.
	LoadingStyleMinimal LoadingStyle = "minimal"

	// LoadingStyleCustom allows injecting custom HTML/CSS directly.
	LoadingStyleCustom LoadingStyle = "custom"
)

// LoadingConfig configures the pre-Flutter loading screen.
type LoadingConfig struct {
	// Enabled determines whether a pre-Flutter loading screen is injected. Default true.
	Enabled bool

	// Style is the loading animation style (Spinner, Pulse, Bar, Dots, Minimal, Custom).
	Style LoadingStyle

	// BrandName is the title text shown on the loading screen (e.g., "Benzat Admin").
	BrandName string

	// Subtitle is an optional secondary text (e.g., "Loading workspace...").
	Subtitle string

	// LogoURL is an optional URL/path to a brand logo or icon.
	LogoURL string

	// BackgroundColor is the CSS background color (e.g. "#0f172a" or "#ffffff").
	BackgroundColor string

	// PrimaryColor is the accent color for spinners/bars (e.g. "#f59e0b" or "#3b82f6").
	PrimaryColor string

	// TextColor is the text color for titles/subtitles (e.g. "#f8fafc").
	TextColor string

	// CustomHTML replaces the entire loader markup if Style is LoadingStyleCustom.
	CustomHTML string
}

// DefaultLoadingConfig returns a polished, dark-theme default loading screen.
func DefaultLoadingConfig() LoadingConfig {
	return LoadingConfig{
		Enabled:         true,
		Style:           LoadingStyleSpinner,
		BrandName:       "",
		Subtitle:        "Loading workspace...",
		LogoURL:         "",
		BackgroundColor: "#0f172a",
		PrimaryColor:    "#f59e0b",
		TextColor:       "#f8fafc",
	}
}

// RenderHTML generates the HTML and CSS snippet to inject into index.html.
// The snippet automatically removes itself once Flutter engine initializes.
func (c LoadingConfig) RenderHTML() string {
	if !c.Enabled {
		return ""
	}

	bg := c.BackgroundColor
	if bg == "" {
		bg = "#0f172a"
	}
	accent := c.PrimaryColor
	if accent == "" {
		accent = "#f59e0b"
	}
	textColor := c.TextColor
	if textColor == "" {
		textColor = "#f8fafc"
	}
	subtitle := c.Subtitle
	if subtitle == "" {
		subtitle = "Loading workspace..."
	}

	if c.Style == LoadingStyleCustom && c.CustomHTML != "" {
		return c.CustomHTML
	}

	var innerContent string
	var extraCSS string

	logoTag := ""
	if c.LogoURL != "" {
		logoTag = fmt.Sprintf(`<img src="%s" class="gfl-logo" alt="%s" />`, c.LogoURL, c.BrandName)
	}

	brandTag := ""
	if c.BrandName != "" {
		brandTag = fmt.Sprintf(`<div class="gfl-brand">%s</div>`, c.BrandName)
	}

	subTag := fmt.Sprintf(`<div class="gfl-sub">%s</div>`, subtitle)

	switch c.Style {
	case LoadingStylePulse:
		extraCSS = `
      .gfl-pulse-ring {
        width: 72px; height: 72px; border-radius: 50%;
        background: ` + accent + `22; border: 2px solid ` + accent + `;
        display: flex; align-items: center; justify-content: center;
        animation: gfl-pulse 1.6s ease-in-out infinite;
        margin-bottom: 20px;
      }
      .gfl-pulse-core {
        width: 36px; height: 36px; border-radius: 50%;
        background: ` + accent + `;
        box-shadow: 0 0 20px ` + accent + `aa;
      }
      @keyframes gfl-pulse {
        0%, 100% { transform: scale(0.92); opacity: 0.7; }
        50% { transform: scale(1.08); opacity: 1; }
      }
    `
		if logoTag != "" {
			innerContent = `<div class="gfl-pulse-ring">` + logoTag + `</div>` + brandTag + subTag
		} else {
			innerContent = `<div class="gfl-pulse-ring"><div class="gfl-pulse-core"></div></div>` + brandTag + subTag
		}

	case LoadingStyleBar:
		extraCSS = `
      .gfl-bar-track {
        width: 180px; height: 4px; background: rgba(255,255,255,0.1);
        border-radius: 4px; overflow: hidden; position: relative; margin-top: 16px;
      }
      .gfl-bar-fill {
        width: 60px; height: 100%; background: ` + accent + `;
        border-radius: 4px; position: absolute;
        animation: gfl-shimmer 1.4s cubic-bezier(0.65, 0, 0.35, 1) infinite;
      }
      @keyframes gfl-shimmer {
        0% { left: -60px; }
        100% { left: 180px; }
      }
    `
		innerContent = logoTag + brandTag + `<div class="gfl-bar-track"><div class="gfl-bar-fill"></div></div>` + subTag

	case LoadingStyleDots:
		extraCSS = `
      .gfl-dots { display: flex; gap: 8px; margin: 16px 0; }
      .gfl-dot {
        width: 10px; height: 10px; border-radius: 50%; background: ` + accent + `;
        animation: gfl-bounce 1.4s ease-in-out infinite both;
      }
      .gfl-dot:nth-child(1) { animation-delay: -0.32s; }
      .gfl-dot:nth-child(2) { animation-delay: -0.16s; }
      @keyframes gfl-bounce {
        0%, 80%, 100% { transform: scale(0); opacity: 0.3; }
        40% { transform: scale(1); opacity: 1; }
      }
    `
		innerContent = logoTag + brandTag + `<div class="gfl-dots"><div class="gfl-dot"></div><div class="gfl-dot"></div><div class="gfl-dot"></div></div>` + subTag

	case LoadingStyleMinimal:
		extraCSS = `
      .gfl-min-spin {
        width: 32px; height: 32px; border: 3px solid rgba(255,255,255,0.1);
        border-top-color: ` + accent + `; border-radius: 50%;
        animation: gfl-spin 0.8s linear infinite;
      }
      @keyframes gfl-spin { to { transform: rotate(360deg); } }
    `
		innerContent = `<div class="gfl-min-spin"></div>`

	default: // LoadingStyleSpinner
		extraCSS = `
      .gfl-spinner-wrap { position: relative; width: 64px; height: 64px; margin-bottom: 20px; }
      .gfl-spinner-ring {
        width: 100%; height: 100%; border-radius: 50%;
        border: 3px solid transparent;
        border-top-color: ` + accent + `;
        border-right-color: ` + accent + `88;
        animation: gfl-spin 0.9s cubic-bezier(0.55, 0.15, 0.45, 0.85) infinite;
      }
      .gfl-spinner-glow {
        position: absolute; inset: 6px; border-radius: 50%;
        background: radial-gradient(circle, ` + accent + `22 0%, transparent 70%);
      }
      @keyframes gfl-spin { to { transform: rotate(360deg); } }
    `
		innerContent = `<div class="gfl-spinner-wrap"><div class="gfl-spinner-glow"></div><div class="gfl-spinner-ring"></div></div>` + logoTag + brandTag + subTag
	}

	return fmt.Sprintf(`<!-- GoLaravel / GoFilament Pre-Engine Loader -->
<div id="gfl-preloader" style="position:fixed;inset:0;z-index:999999;display:flex;flex-direction:column;align-items:center;justify-content:center;background:%s;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;transition:opacity 0.35s ease,visibility 0.35s ease;">
  <style>
    #gfl-preloader .gfl-logo { max-width: 56px; max-height: 56px; margin-bottom: 14px; border-radius: 12px; }
    #gfl-preloader .gfl-brand { font-size: 19px; font-weight: 700; color: %s; letter-spacing: -0.2px; margin-bottom: 4px; }
    #gfl-preloader .gfl-sub { font-size: 12px; color: %s; opacity: 0.7; letter-spacing: 0.3px; margin-top: 6px; }
    %s
  </style>
  %s
</div>
<script>
  (function() {
    function hideGflPreloader() {
      var preloaders = document.querySelectorAll('#gfl-preloader, [id="gfl-preloader"]');
      preloaders.forEach(function(p) {
        p.style.opacity = '0';
        p.style.pointerEvents = 'none';
        setTimeout(function() { if (p && p.parentNode) p.parentNode.removeChild(p); }, 400);
      });
    }
    // Auto-dismiss once Flutter first frame renders or falls back after timeout
    window.addEventListener('flutter-first-frame', hideGflPreloader);
    var checkCount = 0;
    var checkInterval = setInterval(function() {
      checkCount++;
      if (document.querySelector('flutter-view') || document.querySelector('flt-glass-pane') || checkCount > 35) {
        clearInterval(checkInterval);
        setTimeout(hideGflPreloader, 100);
      }
    }, 100);
    // Hard fallback after 3.5s
    setTimeout(hideGflPreloader, 3500);
  })();
</script>`, bg, textColor, textColor, extraCSS, innerContent)
}

// InjectLoadingScreen inserts the loading HTML snippet into an index.html content bytes.
func InjectLoadingScreen(html []byte, config LoadingConfig) []byte {
	if !config.Enabled {
		return html
	}
	snippet := config.RenderHTML()
	if snippet == "" {
		return html
	}

	str := string(html)
	if strings.Contains(str, `<body`) {
		idx := strings.Index(str, "<body")
		endIdx := strings.Index(str[idx:], ">")
		if endIdx != -1 {
			insertPos := idx + endIdx + 1
			return []byte(str[:insertPos] + "\n" + snippet + "\n" + str[insertPos:])
		}
	}
	return append(html, []byte("\n"+snippet)...)
}
