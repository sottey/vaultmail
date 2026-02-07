package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type ThemeOption struct {
	Name  string
	Label string
}

type themeFile struct {
	Name string            `json:"name" yaml:"name"`
	Vars map[string]string `json:"vars" yaml:"vars"`
}

var themeNameRe = regexp.MustCompile(`[^a-z0-9_-]+`)
var themeVarRe = regexp.MustCompile(`[^a-z0-9_-]+`)

func builtinThemes() []ThemeOption {
	return []ThemeOption{
		{Name: "paper", Label: "Paper"},
		{Name: "sage", Label: "Sage"},
		{Name: "slate", Label: "Slate"},
		{Name: "noir", Label: "Noir"},
	}
}

func loadThemes(dir string) (template.CSS, []ThemeOption, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}

	var themes []themeFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".json" && ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		var theme themeFile
		switch ext {
		case ".json":
			err = json.Unmarshal(data, &theme)
		default:
			err = yaml.Unmarshal(data, &theme)
		}
		if err != nil {
			return "", nil, fmt.Errorf("parse theme %s: %w", entry.Name(), err)
		}
		themes = append(themes, theme)
	}

	css, options, err := buildThemeCSS(themes)
	if err != nil {
		return "", nil, err
	}
	return css, options, nil
}

func buildThemeCSS(themes []themeFile) (template.CSS, []ThemeOption, error) {
	if len(themes) == 0 {
		return "", nil, nil
	}
	var options []ThemeOption
	var buf bytes.Buffer

	seen := map[string]bool{}
	for _, theme := range themes {
		name := sanitizeThemeName(theme.Name)
		if name == "" {
			return "", nil, errors.New("theme name is required")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		vars, ok := sanitizeThemeVars(theme.Vars)
		if !ok {
			return "", nil, fmt.Errorf("theme %s has no valid vars", name)
		}

		buf.WriteString("[data-theme=\"")
		buf.WriteString(name)
		buf.WriteString("\"]{\n")
		keys := make([]string, 0, len(vars))
		for key := range vars {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			buf.WriteString("  ")
			buf.WriteString(key)
			buf.WriteString(": ")
			buf.WriteString(vars[key])
			buf.WriteString(";\n")
		}
		buf.WriteString("}\n")

		options = append(options, ThemeOption{Name: name, Label: titleCase(name)})
	}

	sort.Slice(options, func(i, j int) bool { return options[i].Label < options[j].Label })
	return template.CSS(buf.String()), options, nil
}

func sanitizeThemeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	name = themeNameRe.ReplaceAllString(name, "")
	return name
}

func sanitizeThemeVars(vars map[string]string) (map[string]string, bool) {
	if len(vars) == 0 {
		return nil, false
	}
	clean := map[string]string{}
	for key, value := range vars {
		k := strings.ToLower(strings.TrimSpace(key))
		k = strings.TrimPrefix(k, "--")
		k = themeVarRe.ReplaceAllString(k, "")
		if k == "" {
			continue
		}
		v := strings.TrimSpace(value)
		if v == "" || strings.ContainsAny(v, "{};") {
			continue
		}
		clean["--"+k] = v
	}
	if len(clean) == 0 {
		return nil, false
	}
	return clean, true
}

func titleCase(value string) string {
	parts := strings.Split(value, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func loadThemeFile(r io.Reader, ext string) (themeFile, error) {
	var theme themeFile
	data, err := io.ReadAll(r)
	if err != nil {
		return theme, err
	}
	ext = strings.ToLower(ext)
	switch ext {
	case ".json":
		err = json.Unmarshal(data, &theme)
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, &theme)
	default:
		return theme, fmt.Errorf("unsupported theme format")
	}
	return theme, err
}
