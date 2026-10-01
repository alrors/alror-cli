// Package docsite serves Alror's documentation. The Markdown pages are
// embedded in the binary, so `alror docs` and `alror-docs` work offline.
package docsite

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed content/*.md
var content embed.FS

// Page is one entry in the sidebar.
type Page struct {
	Slug, Title, Group string
}

// nav defines order and grouping. Every file in content/ must be listed here.
var nav = []Page{
	{"index", "Overview", "Start"},
	{"quickstart", "Quickstart", "Start"},
	{"cli", "CLI reference", "Use"},
	{"configuration", "alror.yaml", "Use"},
	{"github", "GitHub", "Use"},
	{"connected", "Connected mode", "Use"},
	{"risk-scoring", "Risk scoring", "Concepts"},
	{"rollouts", "Rollouts & verification", "Concepts"},
	{"targets", "Targets & metrics", "Concepts"},
	{"state", "State on the file system", "Concepts"},
	{"console", "Web console", "Use"},
	{"architecture", "Architecture", "Internals"},
}

// Pages returns the navigation entries.
func Pages() []Page { return nav }

var (
	md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	cache sync.Map // slug -> template.HTML
)

func render(slug string) (template.HTML, bool) {
	if v, ok := cache.Load(slug); ok {
		return v.(template.HTML), true
	}
	raw, err := content.ReadFile("content/" + slug + ".md")
	if err != nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := md.Convert(raw, &buf); err != nil {
		return "", false
	}
	h := template.HTML(buf.String())
	cache.Store(slug, h)
	return h, true
}

// Handler serves "/" and "/<slug>".
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.Trim(r.URL.Path, "/")
		if slug == "" {
			slug = "index"
		}
		body, ok := render(slug)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			body = "<h1>Not found</h1><p>No page called <code>" + template.HTML(template.HTMLEscapeString(slug)) + "</code>.</p>"
		}
		title := "Alror docs"
		var prev, next *Page
		for i, p := range nav {
			if p.Slug == slug {
				title = p.Title + " · Alror docs"
				if i > 0 {
					prev = &nav[i-1]
				}
				if i < len(nav)-1 {
					next = &nav[i+1]
				}
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, map[string]any{
			"Title": title, "Body": body, "Slug": slug, "Groups": groups(), "Prev": prev, "Next": next,
		})
	})
	return mux
}

type group struct {
	Name  string
	Pages []Page
}

func groups() []group {
	var out []group
	for _, p := range nav {
		if len(out) == 0 || out[len(out)-1].Name != p.Group {
			out = append(out, group{Name: p.Group})
		}
		out[len(out)-1].Pages = append(out[len(out)-1].Pages, p)
	}
	return out
}

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{--bg:#070708;--surface:#111113;--line:#222226;--fg:#f2f2f3;--muted:#a1a1aa;--faint:#6b6b74;--accent:#35e08f}
*{box-sizing:border-box}html{color-scheme:dark}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.7 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif}
a{color:var(--accent);text-decoration:none}a:hover{text-decoration:underline}
header{position:sticky;top:0;z-index:2;display:flex;align-items:center;gap:16px;height:60px;padding:0 24px;border-bottom:1px solid var(--line);background:rgba(7,7,8,.9);backdrop-filter:blur(10px)}
.logo{display:inline-flex;align-items:center;gap:8px;font-weight:600;letter-spacing:-.03em;font-size:18px;color:var(--fg)}.logo svg{width:24px;height:24px}
.tag{font:11px ui-monospace,Menlo,Consolas,monospace;text-transform:uppercase;letter-spacing:.16em;color:var(--faint)}
#q{margin-left:auto;width:220px;max-width:40vw;padding:7px 12px;border-radius:8px;border:1px solid var(--line);background:var(--surface);color:var(--fg);font:inherit;font-size:14px}
.wrap{display:grid;grid-template-columns:240px minmax(0,1fr);max-width:1180px;margin:0 auto}
nav{position:sticky;top:60px;align-self:start;height:calc(100vh - 60px);overflow:auto;padding:28px 20px;border-right:1px solid var(--line)}
nav h4{margin:22px 0 8px;font:11px ui-monospace,Menlo,Consolas,monospace;text-transform:uppercase;letter-spacing:.16em;color:var(--faint)}
nav h4:first-child{margin-top:0}
nav a{display:block;padding:5px 10px;border-radius:8px;color:var(--muted);font-size:14px}
nav a:hover{background:var(--surface);color:var(--fg);text-decoration:none}
nav a.on{background:var(--surface);color:var(--fg);box-shadow:inset 2px 0 0 var(--accent)}
main{padding:40px 56px 80px;min-width:0}
main h1{font-size:42px;line-height:1.05;letter-spacing:-.04em;margin:0 0 20px}
main h2{font-size:26px;letter-spacing:-.02em;margin:48px 0 12px;padding-top:12px;border-top:1px solid var(--line)}
main h3{font-size:19px;margin:32px 0 8px}
main p,main li{color:#d4d4d8}
code{font:14px ui-monospace,Menlo,Consolas,monospace;background:var(--surface);border:1px solid var(--line);border-radius:6px;padding:1px 6px;color:var(--accent)}
pre{background:#0a0a0b;border:1px solid var(--line);border-radius:14px;padding:16px 20px;overflow:auto;box-shadow:inset 0 2px 6px rgba(0,0,0,.6)}
pre code{background:none;border:0;padding:0;color:#d4d4d8}
table{width:100%;border-collapse:collapse;font-size:14px;margin:16px 0}th,td{text-align:left;padding:9px 12px;border-bottom:1px solid var(--line)}th{color:var(--faint);font-weight:500}
blockquote{margin:16px 0;padding:12px 18px;border-left:2px solid var(--accent);background:var(--surface);border-radius:0 10px 10px 0;color:var(--muted)}
.pager{display:flex;justify-content:space-between;gap:16px;margin-top:64px;padding-top:24px;border-top:1px solid var(--line)}
.pager a{display:block;padding:14px 18px;border:1px solid var(--line);border-radius:14px;background:var(--surface);min-width:200px}.pager small{display:block;color:var(--faint)}
@media(max-width:820px){.wrap{grid-template-columns:1fr}nav{position:static;height:auto;border-right:0;border-bottom:1px solid var(--line)}main{padding:28px 18px 60px}#q{display:none}}
</style></head><body>
<header><a class="logo" href="/"><svg viewBox="0 0 24 24" aria-hidden="true"><rect x=".5" y=".5" width="23" height="23" rx="7" fill="#151518" stroke="#2d2d32"/><path d="M7 18v-7a5 5 0 0 1 10 0v7" fill="none" stroke="#f2f2f3" stroke-width="2.2" stroke-linecap="round"/><circle cx="12" cy="15.5" r="1.9" fill="#35e08f"/></svg>alror</a><span class="tag">docs</span><input id="q" placeholder="Filter pages…" autocomplete="off"></header>
<div class="wrap">
<nav id="nav">{{range .Groups}}<h4>{{.Name}}</h4>{{range .Pages}}<a href="/{{if ne .Slug "index"}}{{.Slug}}{{end}}" class="{{if eq .Slug $.Slug}}on{{end}}">{{.Title}}</a>{{end}}{{end}}</nav>
<main>{{.Body}}
<div class="pager">{{with .Prev}}<a href="/{{if ne .Slug "index"}}{{.Slug}}{{end}}"><small>Previous</small>{{.Title}}</a>{{else}}<span></span>{{end}}{{with .Next}}<a href="/{{.Slug}}" style="text-align:right"><small>Next</small>{{.Title}}</a>{{end}}</div>
</main></div>
<script>
const q=document.getElementById('q');q&&q.addEventListener('input',()=>{const v=q.value.toLowerCase();document.querySelectorAll('#nav a').forEach(a=>{a.style.display=a.textContent.toLowerCase().includes(v)?'':'none'})});
</script></body></html>`))
