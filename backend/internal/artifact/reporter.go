package artifact

import "regexp"

// errorReporter forwards the page's script errors, failed loads and
// console.error calls to the viewer page around it, which reports them to the
// owner's agent (get_artifact). It runs before any of the page's own scripts.
// Messages drop the page's query string: it carries the view token.
const errorReporter = `<script>(function(){if(window.parent===window)return;var n=0;` +
	`function send(m){if(n++>=20)return;m=String(m);if(location.search)m=m.split(location.search).join('');` +
	`try{window.parent.postMessage({agentrove:'error',message:m.slice(0,2000)},'*')}catch(_){}}` +
	`function where(f,l){if(!f||f.split('?')[0]===location.href.split('?')[0])return' (inline script, line '+l+')';return' ('+f.split('?')[0]+':'+l+')'}` +
	`function text(a){if(a&&a.stack)return a.stack;if(typeof a==='object'){try{return JSON.stringify(a)}catch(_){}}return String(a)}` +
	`window.addEventListener('error',function(e){var t=e.target;if(t&&t!==window&&t.tagName){send('Failed to load '+t.tagName.toLowerCase()+' '+(t.src||t.href||''));return}` +
	`send((e.error&&e.error.stack?e.error.stack:e.message)+where(e.filename,e.lineno))},true);` +
	`window.addEventListener('unhandledrejection',function(e){send('Unhandled rejection: '+text(e.reason))});` +
	`var ce=console.error;console.error=function(){send(Array.prototype.map.call(arguments,text).join(' '));return ce.apply(console,arguments)}})();</script>`

var (
	headTag    = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)
	htmlTag    = regexp.MustCompile(`(?i)<html(\s[^>]*)?>`)
	doctypeTag = regexp.MustCompile(`(?i)^\s*<!doctype[^>]*>`)
)

// WithErrorReporter injects errorReporter as early as the document allows
// without knocking it out of standards mode.
func WithErrorReporter(html string) string {
	for _, re := range []*regexp.Regexp{headTag, htmlTag, doctypeTag} {
		if loc := re.FindStringIndex(html); loc != nil {
			return html[:loc[1]] + errorReporter + html[loc[1]:]
		}
	}
	return errorReporter + html
}
