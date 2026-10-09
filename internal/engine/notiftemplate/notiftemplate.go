// Package notiftemplate loads and renders notification templates
// (manifest-spec.md §13a "Template file format"): discovering which locale
// variants of a declared channel template exist inside a module's package,
// validating that the mandatory "en" fallback is present, converting them
// to notification_templates rows, and rendering a row's columns against
// engine-injected variables plus a notification type's own data_schema
// fields (notification-system.md §5).
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
	"slices"
	"strings"
	texttemplate "text/template"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/rs/zerolog/log"
)

// executor is satisfied by both *html/template.Template and
// *text/template.Template — they share this method signature but no
// common exported interface.
type executor interface {
	Execute(w io.Writer, data any) error
}

// Template is one channel's template: each locale variant's raw file
// content discovered in the module's package, keyed by locale.
type Template struct {
	path    string
	Ext     string
	Sources map[string][]byte
}

// Email sibling templates: an email channel's declared .html template may
// have a .json subject ({"subject": "..."}) and a .txt plain-text body
// next to it (notification-system.md §5 "Template format"), resolved
// under these pseudo-channels.
const (
	ChannelEmailSubject = "email_subject"
	ChannelEmailText    = "email_text"
)

// ModuleTemplates holds one module's resolved notification templates,
// keyed "{notificationType}.{channel}".
type ModuleTemplates struct {
	templates map[string]*Template
}

// Files returns the resolved package paths and contents, including email siblings.
func (mt *ModuleTemplates) Files() map[string][]byte {
	files := map[string][]byte{}
	if mt == nil {
		return files
	}

	for _, template := range mt.templates {
		for locale, source := range template.Sources {
			files[strings.Replace(template.path, "{locale}", locale, 1)] = source
		}
	}

	return files
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
	return load(notifTypes, src)
}

// LoadFS is Load for templates in fsys rather than a module package — the
// engine's own, which ship embedded in its binary (notification-system.md
// §6 "Engine-declared notification types").
func LoadFS(notifTypes []manifest.NotificationType, fsys fs.FS) (*ModuleTemplates, error) {
	if len(notifTypes) == 0 {
		return nil, nil
	}
	return load(notifTypes, fsSource{fsys})
}

func load(notifTypes []manifest.NotificationType, src fileSource) (*ModuleTemplates, error) {
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

			if channel != "email" || !strings.HasSuffix(declared, ".html") {
				continue
			}
			base := strings.TrimSuffix(declared, ".html")
			for sibling, path := range map[string]string{ChannelEmailSubject: base + ".json", ChannelEmailText: base + ".txt"} {
				tmpl, err := resolveOptionalChannel(src, names, sibling, path)
				if err != nil {
					return nil, fmt.Errorf("notification type %q, channel %q: %w", nt.Name, sibling, err)
				}
				if tmpl != nil {
					templates[nt.Name+"."+sibling] = tmpl
				}
			}
		}
	}

	return &ModuleTemplates{templates: templates}, nil
}

// LocaleCandidates are the locales a template is looked up in for
// userLocale, closest first (notification-system.md §5 "Locale
// fallback"): the exact locale, its language, then "en".
func LocaleCandidates(userLocale string) []string {
	candidates := make([]string, 0, 3)
	if userLocale != "" {
		candidates = append(candidates, userLocale)
	}
	if lang, _, ok := strings.Cut(userLocale, "-"); ok && lang != "" {
		candidates = append(candidates, lang)
	}
	if !slices.Contains(candidates, "en") {
		candidates = append(candidates, "en")
	}
	return candidates
}

// ParseColumn parses src, the content of notification_templates column
// col, the way a send renders it: html_template as an html/template, every
// other column as a text/template.
func ParseColumn(col, src string) error {
	_, err := parseColumn(col, src)
	return err
}

// RenderColumn parses src, the content of notification_templates column
// col, and executes it against vars — the caller's data_schema fields and
// the 7 engine-injected variables (notification-system.md §5 "Standard
// template variables").
func RenderColumn(col, src string, vars map[string]any) (string, error) {
	exec, err := parseColumn(col, src)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := exec.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute %s: %w", col, err)
	}
	return buf.String(), nil
}

func parseColumn(col, src string) (executor, error) {
	ext := "txt"
	if col == ColHTML {
		ext = "html"
	}
	exec, err := parseTemplate(ext, []byte(src))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", col, err)
	}
	return exec, nil
}

// resolveOptionalChannel is resolveChannel for a template the package may
// leave out entirely: it returns nil when no locale variant of declared
// exists, and still requires "en" when any does.
func resolveOptionalChannel(src fileSource, names []string, channel, declared string) (*Template, error) {
	re, err := compileLocalePattern(declared)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(names, re.MatchString) {
		return nil, nil
	}
	return resolveChannel(src, names, channel, declared)
}

// ReadPackageFile reads the member name, a slash-separated path, from the
// module package at packagePath (a .erp package file or a loose module
// directory).
func ReadPackageFile(packagePath, name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, fmt.Errorf("invalid package path %q", name)
	}
	src, closeSrc, err := openSource(packagePath)
	if err != nil {
		return nil, err
	}
	defer closeSrc()
	return src.read(name)
}

// resolveChannel requires an English locale and parses each available variant. SMS length
// warnings use raw template runes because rendering data is unavailable at load time.
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
	for locale, data := range locales {
		if err := parseChannelSource(channel, ext, data); err != nil {
			return nil, fmt.Errorf("parse %s (%s): %w", declared, locale, err)
		}
	}

	return &Template{path: declared, Ext: ext, Sources: locales}, nil
}

func parseChannelSource(channel, ext string, data []byte) error {
	if ext != "json" {
		_, err := parseTemplate(ext, data)
		return err
	}

	columns := inAppColumns
	switch channel {
	case "push":
		columns = pushColumns
	case ChannelEmailSubject:
		columns = map[string]string{"subject": ColSubject}
	}

	row := Row{Fields: map[string]string{}}
	if err := addJSONFields(&row, data, columns); err != nil {
		return err
	}
	for column, source := range row.Fields {
		if err := ParseColumn(column, source); err != nil {
			return err
		}
	}

	return nil
}

func compileLocalePattern(declared string) (*regexp.Regexp, error) {
	if !fs.ValidPath(declared) || strings.Contains(declared, "\\") || strings.Count(declared, "{locale}") > 1 {
		return nil, fmt.Errorf("templates path %q must be a package path with exactly one {locale} placeholder", declared)
	}
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

// fileSource reads either a .erp archive or a loose module directory.
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

type fsSource struct {
	fsys fs.FS
}

func (f fsSource) list() ([]string, error) {
	var names []string
	err := fs.WalkDir(f.fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			names = append(names, path)
		}
		return nil
	})
	return names, err
}

func (f fsSource) read(name string) ([]byte, error) {
	return fs.ReadFile(f.fsys, name)
}
