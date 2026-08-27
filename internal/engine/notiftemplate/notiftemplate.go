// Package notiftemplate resolves and renders a module's notification
// templates (manifest-spec.md §13a "Template file format"): discovering
// which locale variants of a declared channel template actually exist
// inside a module's package, validating that the mandatory "en" fallback
// is present, and rendering a resolved template against engine-injected
// variables plus a notification type's own data_schema fields
// (notification-system.md §5).
package notiftemplate

import (
	"archive/zip"
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	texttemplate "text/template"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/rs/zerolog/log"
)

// executor is satisfied by both *html/template.Template and
// *text/template.Template — they share this method signature but no
// common exported interface. Safe for concurrent use once parsed, since
// nothing here mutates a template after Load returns.
type executor interface {
	Execute(w io.Writer, data any) error
}

// Template is one channel's parsed template, one entry per locale variant
// discovered in the module's package.
type Template struct {
	Ext     string
	Locales map[string]executor
}

// ModuleTemplates holds one module's resolved notification templates,
// keyed "{notificationType}.{channel}".
type ModuleTemplates struct {
	templates map[string]*Template
}

// Load discovers, validates, and parses notifTypes' declared templates
// from packagePath (a .erp package file or a loose module directory).
// Returns (nil, nil) when notifTypes is empty — nothing to load, distinct
// from a load failure. Returns an error when a declared channel's "en"
// variant is missing from the package, or a template fails to parse; the
// caller is expected to treat that as a load-time validation failure the
// same way any other one is handled (module.LoadedModule.Fail).
func Load(notifTypes []manifest.NotificationType, packagePath string) (*ModuleTemplates, error) {
	if len(notifTypes) == 0 {
		return nil, nil
	}

	src, closeSrc, err := openSource(packagePath)
	if err != nil {
		return nil, err
	}
	defer closeSrc()

	names, err := src.list()
	if err != nil {
		return nil, fmt.Errorf("list package contents: %w", err)
	}

	templates := make(map[string]*Template)
	for _, nt := range notifTypes {
		for channel, declared := range nt.Templates {
			tmpl, err := resolveChannel(src, names, channel, declared)
			if err != nil {
				return nil, fmt.Errorf("notification type %q, channel %q: %w", nt.Name, channel, err)
			}
			templates[nt.Name+"."+channel] = tmpl
		}
	}

	return &ModuleTemplates{templates: templates}, nil
}

// Resolve applies the 3-step locale fallback (notification-system.md §5:
// exact match, language-only match, "en" default) and returns the
// matched locale and template. ok is false, and the miss is logged, when
// none of the three match anything Load discovered — the caller (e.g. a
// future notify.Send) decides whether to skip the channel.
func (mt *ModuleTemplates) Resolve(notificationType, channel, userLocale string) (string, *Template, bool) {
	if mt == nil {
		return "", nil, false
	}

	tmpl, ok := mt.templates[notificationType+"."+channel]
	if !ok {
		return "", nil, false
	}

	candidates := make([]string, 0, 3)
	candidates = append(candidates, userLocale)
	if idx := strings.Index(userLocale, "-"); idx > 0 {
		candidates = append(candidates, userLocale[:idx])
	}
	candidates = append(candidates, "en")

	for _, c := range candidates {
		if _, ok := tmpl.Locales[c]; ok {
			return c, tmpl, true
		}
	}

	log.Warn().
		Str("notification_type", notificationType).
		Str("channel", channel).
		Str("locale", userLocale).
		Msg("no notification template variant matched exact, language, or en fallback locale")
	return "", nil, false
}

// Render executes tmpl's parsed template for locale against vars — the
// caller's merged data_schema fields and the 7 engine-injected variables
// (notification-system.md §5 "Standard template variables"), addressed
// uniformly via {{.Key}} since Go's template dot-notation resolves map
// keys the same way it resolves struct fields.
func Render(tmpl *Template, locale string, vars map[string]any) (string, error) {
	exec, ok := tmpl.Locales[locale]
	if !ok {
		return "", fmt.Errorf("locale %q not present in this template", locale)
	}

	var buf bytes.Buffer
	if err := exec.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// resolveChannel discovers every locale variant of declared (a path
// containing a literal "{locale}" placeholder) present among names,
// requires "en" among them, warns on an over-length raw sms source
// (notification-system.md §5 — rune count of the raw template, not
// rendered output, checked here at load time since no per-notification
// data exists yet to render against), and parses each variant.
func resolveChannel(src fileSource, names []string, channel, declared string) (*Template, error) {
	re, err := compileLocalePattern(declared)
	if err != nil {
		return nil, err
	}

	locales := make(map[string][]byte)
	for _, name := range names {
		m := re.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		data, err := src.read(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		locales[m[1]] = data
	}

	enSource, ok := locales["en"]
	if !ok {
		return nil, fmt.Errorf("declared template %q has no \"en\" variant in the package", declared)
	}

	if channel == "sms" {
		if n := utf8.RuneCountInString(string(enSource)); n > 160 {
			log.Warn().
				Str("channel", channel).
				Str("template", declared).
				Int("length", n).
				Msg("SMS template exceeds the documented 160-character guideline")
		}
	}

	ext := strings.TrimPrefix(filepath.Ext(declared), ".")
	parsed := make(map[string]executor, len(locales))
	for locale, data := range locales {
		exec, err := parseTemplate(ext, data)
		if err != nil {
			return nil, fmt.Errorf("parse %s (%s): %w", declared, locale, err)
		}
		parsed[locale] = exec
	}

	return &Template{Ext: ext, Locales: parsed}, nil
}

// compileLocalePattern turns a manifest-declared path containing exactly
// one literal "{locale}" placeholder into a regexp matching real package
// member paths, capturing the locale segment.
func compileLocalePattern(declared string) (*regexp.Regexp, error) {
	parts := strings.SplitN(declared, "{locale}", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("templates path %q has no {locale} placeholder", declared)
	}
	pattern := "^" + regexp.QuoteMeta(parts[0]) + "([^/]+)" + regexp.QuoteMeta(parts[1]) + "$"
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern for %q: %w", declared, err)
	}
	return re, nil
}

func parseTemplate(ext string, data []byte) (executor, error) {
	if ext == "html" {
		return htmltemplate.New("").Parse(string(data))
	}
	return texttemplate.New("").Parse(string(data))
}

// fileSource abstracts reading a module's package contents, whether it's
// a real .erp zip archive or a loose module directory — the two shapes
// module.LoadedModule.PackagePath can point at (goerp#425).
type fileSource interface {
	list() ([]string, error)
	read(name string) ([]byte, error)
}

func openSource(packagePath string) (fileSource, func(), error) {
	info, err := os.Stat(packagePath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat package: %w", err)
	}
	if info.IsDir() {
		return &dirSource{root: packagePath}, func() {}, nil
	}

	r, err := zip.OpenReader(packagePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open package: %w", err)
	}
	return &zipSource{r: r}, func() { _ = r.Close() }, nil
}

type zipSource struct {
	r *zip.ReadCloser
}

func (z *zipSource) list() ([]string, error) {
	names := make([]string, len(z.r.File))
	for i, f := range z.r.File {
		names[i] = f.Name
	}
	return names, nil
}

func (z *zipSource) read(name string) ([]byte, error) {
	for _, f := range z.r.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("member %q not found", name)
}

type dirSource struct {
	root string
}

func (d *dirSource) list() ([]string, error) {
	var names []string
	err := filepath.WalkDir(d.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(d.root, path)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	return names, err
}

func (d *dirSource) read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.root, filepath.FromSlash(name)))
}
