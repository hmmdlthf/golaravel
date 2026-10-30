package mail

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

type previewEntry struct {
	name    string
	builder func(ctx context.Context) (*Message, error)
}

var (
	previewMu sync.RWMutex
	previews  = make(map[string]previewEntry)
)

// RegisterPreview registers a named email preview for the Mail Preview Studio.
// Target can be a Mailable, func(ctx context.Context) (*Message, error), func() (*Message, error), or a *Message.
func RegisterPreview(name string, target any) {
	previewMu.Lock()
	defer previewMu.Unlock()

	var builder func(ctx context.Context) (*Message, error)

	switch v := target.(type) {
	case Mailable:
		builder = v.Build
	case func(ctx context.Context) (*Message, error):
		builder = v
	case func() (*Message, error):
		builder = func(ctx context.Context) (*Message, error) {
			return v()
		}
	case *Message:
		builder = func(ctx context.Context) (*Message, error) {
			return v, nil
		}
	default:
		panic(fmt.Sprintf("mail.RegisterPreview: unsupported target type %T", target))
	}

	previews[name] = previewEntry{
		name:    name,
		builder: builder,
	}
}

// ClearPreviews removes all registered previews (useful in tests).
func ClearPreviews() {
	previewMu.Lock()
	defer previewMu.Unlock()
	previews = make(map[string]previewEntry)
}

// GetPreviewNames returns a sorted list of registered preview names.
func GetPreviewNames() []string {
	previewMu.RLock()
	defer previewMu.RUnlock()

	names := make([]string, 0, len(previews))
	for k := range previews {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// RenderPreview executes the builder for the specified preview name.
func RenderPreview(ctx context.Context, name string) (*Message, error) {
	previewMu.RLock()
	entry, ok := previews[name]
	previewMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mail: preview '%s' not found", name)
	}

	return entry.builder(ctx)
}

// MountPreviewStudio mounts the interactive Mail Preview Studio routes.
func MountPreviewStudio(r router.RouteRegistrar, prefix ...string) {
	base := "/mail/preview"
	if len(prefix) > 0 && prefix[0] != "" {
		base = strings.TrimRight(prefix[0], "/")
	}

	// 1. Studio Web Dashboard
	r.Get(base, func(c *gcontext.Context) error {
		names := GetPreviewNames()
		selected := c.Query("mail")
		if selected == "" && len(names) > 0 {
			selected = names[0]
		}

		var currentMsg *Message
		var buildErr error
		if selected != "" {
			currentMsg, buildErr = RenderPreview(c.Request.Context(), selected)
		}

		dashboardHTML := renderStudioDashboard(base, names, selected, currentMsg, buildErr)
		return c.HTML(http.StatusOK, dashboardHTML)
	})

	// 2. Render Live Isolated HTML in Iframe
	r.Get(base+"/render", func(c *gcontext.Context) error {
		name := c.Query("mail")
		if name == "" {
			return c.HTML(http.StatusBadRequest, "<h3>Missing 'mail' query parameter</h3>")
		}

		msg, err := RenderPreview(c.Request.Context(), name)
		if err != nil {
			return c.HTML(http.StatusInternalServerError, fmt.Sprintf("<div style='color:red;padding:20px;font-family:sans-serif;'><h3>Failed to render preview:</h3><pre>%v</pre></div>", err))
		}

		if msg.HTMLBody != "" {
			return c.HTML(http.StatusOK, msg.HTMLBody)
		}
		// Fallback to text formatted in pre
		return c.HTML(http.StatusOK, fmt.Sprintf("<html><body style='font-family:monospace;padding:20px;white-space:pre-wrap;'>%s</body></html>", html.EscapeString(msg.TextBody)))
	})

	// 3. Raw Text / MIME / JSON inspection
	r.Get(base+"/raw", func(c *gcontext.Context) error {
		name := c.Query("mail")
		format := c.Query("format") // "text", "mime", or "json"

		msg, err := RenderPreview(c.Request.Context(), name)
		if err != nil {
			return c.String(http.StatusInternalServerError, "%s", err.Error())
		}

		if format == "mime" {
			mimeBytes, err := msg.BuildMIME()
			if err != nil {
				return c.String(http.StatusInternalServerError, "%s", err.Error())
			}
			return c.String(http.StatusOK, "%s", string(mimeBytes))
		}

		if format == "json" {
			return c.JSON(http.StatusOK, msg)
		}

		return c.String(http.StatusOK, "%s", msg.TextBody)
	})

	// 4. API endpoint listing all previews
	r.Get(base+"/api/list", func(c *gcontext.Context) error {
		names := GetPreviewNames()
		type previewMeta struct {
			Name        string   `json:"name"`
			Subject     string   `json:"subject"`
			To          []string `json:"to"`
			HasHTML     bool     `json:"has_html"`
			HasText     bool     `json:"has_text"`
			Attachments int      `json:"attachments"`
			Error       string   `json:"error,omitempty"`
		}

		var list []previewMeta
		for _, name := range names {
			meta := previewMeta{Name: name}
			if msg, err := RenderPreview(c.Request.Context(), name); err == nil {
				meta.Subject = msg.SubjectLine
				meta.To = msg.ToAddresses
				meta.HasHTML = msg.HTMLBody != ""
				meta.HasText = msg.TextBody != ""
				meta.Attachments = len(msg.Attachments)
			} else {
				meta.Error = err.Error()
			}
			list = append(list, meta)
		}

		return c.JSON(http.StatusOK, list)
	})
}

func renderStudioDashboard(base string, names []string, selected string, msg *Message, buildErr error) string {
	var navItems strings.Builder
	for _, name := range names {
		activeClass := "hover:bg-slate-800 text-slate-300"
		activeIndicator := ""
		if name == selected {
			activeClass = "bg-red-600/10 text-red-400 font-semibold border-l-4 border-red-500"
			activeIndicator = `<span class="w-2 h-2 rounded-full bg-red-400 animate-pulse ml-auto"></span>`
		}
		navItems.WriteString(fmt.Sprintf(`
			<a href="%s?mail=%s" class="flex items-center px-4 py-3 text-sm rounded-lg transition-colors %s">
				<span class="truncate">%s</span>
				%s
			</a>
		`, base, url.QueryEscape(name), activeClass, html.EscapeString(name), activeIndicator))
	}

	noPreviewsNotice := ""
	if len(names) == 0 {
		noPreviewsNotice = `
			<div class="p-6 text-center text-slate-400">
				<svg class="w-12 h-12 mx-auto mb-3 text-slate-600" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"></path></svg>
				<p class="font-medium text-slate-300">No Mailables Registered</p>
				<p class="text-xs text-slate-500 mt-1">Register previews with <code>mail.RegisterPreview("Name", mailable)</code></p>
			</div>
		`
	}

	headerDetails := ""
	iframeSrc := ""
	rawTextLink := ""
	rawMIMELink := ""

	if msg != nil {
		iframeSrc = fmt.Sprintf("%s/render?mail=%s", base, url.QueryEscape(selected))
		rawTextLink = fmt.Sprintf("%s/raw?mail=%s&format=text", base, url.QueryEscape(selected))
		rawMIMELink = fmt.Sprintf("%s/raw?mail=%s&format=mime", base, url.QueryEscape(selected))

		toStr := strings.Join(msg.ToAddresses, ", ")
		if toStr == "" {
			toStr = "(no recipients specified)"
		}
		ccStr := ""
		if len(msg.CcAddresses) > 0 {
			ccStr = fmt.Sprintf(`<span class="text-xs text-slate-400 ml-3">CC: <strong class="text-slate-200">%s</strong></span>`, html.EscapeString(strings.Join(msg.CcAddresses, ", ")))
		}

		attachmentsBadge := ""
		if len(msg.Attachments) > 0 {
			var attList []string
			for _, a := range msg.Attachments {
				attList = append(attList, html.EscapeString(a.Filename))
			}
			attachmentsBadge = fmt.Sprintf(`
				<div class="mt-2 flex items-center gap-2 text-xs text-amber-400 bg-amber-950/40 border border-amber-800/40 rounded px-2.5 py-1 w-fit">
					<span>📎 %d Attachment(s): %s</span>
				</div>
			`, len(msg.Attachments), strings.Join(attList, ", "))
		}

		headerDetails = fmt.Sprintf(`
			<div class="bg-slate-900 border-b border-slate-800 px-6 py-4">
				<div class="flex items-center justify-between mb-2">
					<h2 class="text-lg font-bold text-white tracking-tight flex items-center gap-2">
						<span class="text-red-500 font-mono text-base">Subject:</span>
						%s
					</h2>
					<div class="flex items-center gap-2">
						<a href="%s" target="_blank" class="text-xs text-slate-400 hover:text-white bg-slate-800 hover:bg-slate-700 px-2.5 py-1.5 rounded transition">↗ Open Raw HTML</a>
						<a href="%s" target="_blank" class="text-xs text-slate-400 hover:text-white bg-slate-800 hover:bg-slate-700 px-2.5 py-1.5 rounded transition">Plain Text</a>
						<a href="%s" target="_blank" class="text-xs text-slate-400 hover:text-white bg-slate-800 hover:bg-slate-700 px-2.5 py-1.5 rounded transition">RFC MIME</a>
					</div>
				</div>
				<div class="text-xs text-slate-400 flex flex-wrap items-center gap-y-1">
					<span>From: <strong class="text-slate-200">%s</strong></span>
					<span class="mx-2 text-slate-600">•</span>
					<span>To: <strong class="text-slate-200">%s</strong></span>
					%s
				</div>
				%s
			</div>
		`, html.EscapeString(msg.SubjectLine), iframeSrc, rawTextLink, rawMIMELink,
			html.EscapeString(msg.FromAddress), html.EscapeString(toStr), ccStr, attachmentsBadge)
	} else if buildErr != nil {
		headerDetails = fmt.Sprintf(`
			<div class="bg-red-950/50 border-b border-red-800/50 px-6 py-4 text-red-200">
				<h3 class="font-bold">Error building preview for "%s":</h3>
				<pre class="mt-2 text-xs font-mono text-red-400 whitespace-pre-wrap">%v</pre>
			</div>
		`, html.EscapeString(selected), buildErr)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en" class="h-full bg-slate-950 text-slate-100">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>GoLaravel • Mail Preview Studio</title>
	<script src="https://cdn.tailwindcss.com"></script>
	<style>
		.device-desktop { width: 100%%; max-width: 100%%; }
		.device-tablet  { width: 768px; }
		.device-mobile  { width: 375px; }
	</style>
</head>
<body class="h-full flex flex-col font-sans antialiased overflow-hidden">
	<!-- Top Navigation Bar -->
	<header class="bg-slate-900 border-b border-slate-800 px-6 py-3 flex items-center justify-between z-10">
		<div class="flex items-center gap-3">
			<span class="text-xl">📧</span>
			<span class="font-black text-lg tracking-wider text-white">GoLaravel <span class="text-red-500 font-light text-sm uppercase px-1.5 py-0.5 rounded bg-red-500/10 border border-red-500/20">Mail Studio</span></span>
		</div>

		<!-- Viewport Device Switcher -->
		<div class="flex items-center bg-slate-800/80 p-1 rounded-lg border border-slate-700/60 text-xs font-medium text-slate-400">
			<button onclick="setDevice('desktop')" id="btn-desktop" class="px-3 py-1.5 rounded-md transition text-white bg-slate-700 shadow flex items-center gap-1.5">
				<span>🖥</span> Desktop
			</button>
			<button onclick="setDevice('tablet')" id="btn-tablet" class="px-3 py-1.5 rounded-md transition hover:text-white flex items-center gap-1.5">
				<span>📱</span> Tablet (768px)
			</button>
			<button onclick="setDevice('mobile')" id="btn-mobile" class="px-3 py-1.5 rounded-md transition hover:text-white flex items-center gap-1.5">
				<span>📲</span> Mobile (375px)
			</button>
		</div>

		<div class="text-xs text-slate-400 flex items-center gap-4">
			<span>%d preview(s) loaded</span>
		</div>
	</header>

	<!-- Main Workspace -->
	<div class="flex-1 flex overflow-hidden">
		<!-- Sidebar Navigation -->
		<aside class="w-72 bg-slate-900/60 border-r border-slate-800 flex flex-col overflow-y-auto">
			<div class="p-4 border-b border-slate-800/60">
				<h3 class="text-xs font-bold text-slate-400 uppercase tracking-wider mb-2">Registered Mailables</h3>
				<input type="text" id="search" placeholder="Search templates..." class="w-full bg-slate-950 border border-slate-800 rounded-md px-3 py-1.5 text-xs text-slate-200 placeholder-slate-500 focus:outline-none focus:border-red-500 transition">
			</div>
			<nav id="nav-list" class="p-2 space-y-1">
				%s
				%s
			</nav>
		</aside>

		<!-- Email Stage -->
		<main class="flex-1 flex flex-col bg-slate-950 overflow-hidden">
			%s

			<!-- Device Container Frame -->
			<div class="flex-1 overflow-auto p-4 flex justify-center bg-slate-950/80">
				<div id="frame-container" class="device-desktop h-full bg-white rounded-lg shadow-2xl transition-all duration-300 overflow-hidden flex flex-col border border-slate-800">
					%s
				</div>
			</div>
		</main>
	</div>

	<script>
		function setDevice(device) {
			const container = document.getElementById('frame-container');
			const btnDesktop = document.getElementById('btn-desktop');
			const btnTablet = document.getElementById('btn-tablet');
			const btnMobile = document.getElementById('btn-mobile');

			[btnDesktop, btnTablet, btnMobile].forEach(b => {
				b.className = 'px-3 py-1.5 rounded-md transition hover:text-white flex items-center gap-1.5';
			});

			container.classList.remove('device-desktop', 'device-tablet', 'device-mobile');
			container.classList.add('device-' + device);

			const activeBtn = document.getElementById('btn-' + device);
			activeBtn.className = 'px-3 py-1.5 rounded-md transition text-white bg-slate-700 shadow flex items-center gap-1.5';
		}

		// Filter templates
		document.getElementById('search').addEventListener('input', function(e) {
			const term = e.target.value.toLowerCase();
			document.querySelectorAll('#nav-list a').forEach(a => {
				const text = a.innerText.toLowerCase();
				a.style.display = text.includes(term) ? 'flex' : 'none';
			});
		});
	</script>
</body>
</html>`, len(names), navItems.String(), noPreviewsNotice, headerDetails,
		func() string {
			if iframeSrc != "" {
				return fmt.Sprintf(`<iframe id="preview-iframe" src="%s" class="w-full h-full border-0"></iframe>`, iframeSrc)
			}
			return `<div class="h-full flex items-center justify-center text-slate-400 text-sm font-sans">Select a mailable from the sidebar to preview.</div>`
		}())
}
