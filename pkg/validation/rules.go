package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Rules maps a field (dot path, "*" for every element: "items.*.name") to
// a Laravel-style rule string ("required|string|max:255") or a list of
// rule strings and Rule values.
type Rules map[string]any

// Rule is a custom rule usable inside a rule list.
type Rule interface {
	// Passes reports whether value is valid for attribute.
	Passes(ctx context.Context, attribute string, value any, data map[string]any) bool
	// Message is the error message; ":attribute" is replaced.
	Message() string
}

// RuleFunc adapts a function into a Rule.
func RuleFunc(message string, fn func(value any) bool) Rule { return ruleFunc{msg: message, fn: fn} }

type ruleFunc struct {
	msg string
	fn  func(any) bool
}

func (r ruleFunc) Passes(_ context.Context, _ string, v any, _ map[string]any) bool { return r.fn(v) }
func (r ruleFunc) Message() string                                                  { return r.msg }

// DBChecker answers the database rules (unique, exists). See SQLChecker.
type DBChecker interface {
	// Count returns how many rows of table have column = value, leaving out
	// rows whose ignoreColumn equals ignoreValue (when ignoreColumn != "").
	Count(ctx context.Context, table, column string, value any, ignoreColumn string, ignoreValue any) (int64, error)
}

// RulesValidator validates a data map against Rules.
type RulesValidator struct {
	ctx        context.Context
	data       map[string]any
	rules      Rules
	messages   map[string]string
	attributes map[string]string
	translate  func(key string, params map[string]any) string
	db         DBChecker
	errors     ErrorBag
	validated  map[string]any
	ran        bool
}

// Make creates a validator for data. Nothing runs until Fails / Validate.
func Make(data map[string]any, rules Rules) *RulesValidator {
	if data == nil {
		data = map[string]any{}
	}
	return &RulesValidator{ctx: context.Background(), data: data, rules: rules, errors: ErrorBag{}}
}

// WithContext sets the context passed to database checks and custom rules.
func (v *RulesValidator) WithContext(ctx context.Context) *RulesValidator {
	v.ctx = ctx
	return v
}

// Messages overrides messages: keyed by "field.rule" ("email.unique"),
// "field" (any rule of that field) or "rule" (every field).
func (v *RulesValidator) Messages(m map[string]string) *RulesValidator {
	v.messages = m
	return v
}

// Attributes sets display names for fields ("dob" -> "date of birth").
func (v *RulesValidator) Attributes(a map[string]string) *RulesValidator {
	v.attributes = a
	return v
}

// Translator looks messages up as "validation.<rule>" (for example from
// lang/<locale>/validation.json); when fn returns the key unchanged the
// built-in English message is used.
func (v *RulesValidator) Translator(fn func(key string, params map[string]any) string) *RulesValidator {
	v.translate = fn
	return v
}

// DB sets the checker used by unique and exists.
func (v *RulesValidator) DB(db DBChecker) *RulesValidator {
	v.db = db
	return v
}

// Fails runs the rules and reports whether any failed.
func (v *RulesValidator) Fails() bool {
	v.run()
	return len(v.errors) > 0
}

// Passes is !Fails().
func (v *RulesValidator) Passes() bool { return !v.Fails() }

// Errors returns the error bag (runs the rules first if needed).
func (v *RulesValidator) Errors() ErrorBag {
	v.run()
	return v.errors
}

// Validate runs the rules and returns a *ValidationError when any failed.
func (v *RulesValidator) Validate() error {
	if v.Fails() {
		return &ValidationError{Message: "The given data was invalid.", Errors: v.errors}
	}
	return nil
}

// Validated returns only the data covered by rules (nested paths kept),
// for use after Validate succeeds: unknown input never reaches your code.
func (v *RulesValidator) Validated() map[string]any {
	v.run()
	return v.validated
}

// ---------------------------------------------------------------------------
// Rule parsing

type parsedRule struct {
	name   string
	params []string
	custom Rule
}

func parseRules(spec any) ([]parsedRule, error) {
	var parts []any
	switch s := spec.(type) {
	case string:
		for _, p := range splitRules(s) {
			parts = append(parts, p)
		}
	case []string:
		for _, p := range s {
			parts = append(parts, p)
		}
	case []any:
		parts = s
	case Rule:
		parts = []any{s}
	default:
		return nil, fmt.Errorf("validation: unsupported rule spec %T", spec)
	}
	out := make([]parsedRule, 0, len(parts))
	for _, p := range parts {
		switch r := p.(type) {
		case Rule:
			out = append(out, parsedRule{name: "custom", custom: r})
		case string:
			for _, piece := range splitRules(r) {
				name, arg, has := strings.Cut(piece, ":")
				pr := parsedRule{name: strings.ToLower(strings.TrimSpace(name))}
				if has {
					if pr.name == "regex" || pr.name == "not_regex" || pr.name == "date_format" {
						pr.params = []string{arg}
					} else {
						pr.params = strings.Split(arg, ",")
					}
				}
				out = append(out, pr)
			}
		default:
			return nil, fmt.Errorf("validation: unsupported rule %T", p)
		}
	}
	return out, nil
}

// splitRules splits on "|" except inside a regex:/not_regex: argument,
// which runs to the end of the string (use a rule list for regexes with |).
func splitRules(s string) []string {
	var out []string
	for s != "" {
		lower := strings.ToLower(strings.TrimLeft(s, " "))
		if strings.HasPrefix(lower, "regex:") || strings.HasPrefix(lower, "not_regex:") {
			out = append(out, strings.TrimSpace(s))
			break
		}
		part, rest, _ := strings.Cut(s, "|")
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
		s = rest
	}
	return out
}

func hasRule(rules []parsedRule, names ...string) bool {
	for _, r := range rules {
		for _, n := range names {
			if r.name == n {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Data access

// get returns the value at a dot path and whether the key exists.
func get(data any, path string) (any, bool) {
	cur := data
	for _, seg := range strings.Split(path, ".") {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			rv := reflect.ValueOf(cur)
			if rv.Kind() == reflect.Slice {
				i, err := strconv.Atoi(seg)
				if err != nil || i < 0 || i >= rv.Len() {
					return nil, false
				}
				cur = rv.Index(i).Interface()
				continue
			}
			return nil, false
		}
	}
	return cur, true
}

// expand turns a pattern with "*" into the concrete paths present in data.
func expand(data map[string]any, pattern string) []string {
	if !strings.Contains(pattern, "*") {
		return []string{pattern}
	}
	segs := strings.Split(pattern, ".")
	paths := []string{""}
	for _, seg := range segs {
		var next []string
		for _, p := range paths {
			if seg != "*" {
				next = append(next, join(p, seg))
				continue
			}
			parent, ok := any(data), true
			if p != "" {
				parent, ok = get(data, p)
			}
			if !ok {
				continue
			}
			switch c := parent.(type) {
			case []any:
				for i := range c {
					next = append(next, join(p, strconv.Itoa(i)))
				}
			case map[string]any:
				keys := make([]string, 0, len(c))
				for k := range c {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					next = append(next, join(p, k))
				}
			default:
				rv := reflect.ValueOf(parent)
				if rv.Kind() == reflect.Slice {
					for i := 0; i < rv.Len(); i++ {
						next = append(next, join(p, strconv.Itoa(i)))
					}
				}
			}
		}
		paths = next
	}
	return paths
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

func set(dst map[string]any, path string, val any) {
	segs := strings.Split(path, ".")
	cur := dst
	for i, seg := range segs {
		if i == len(segs)-1 {
			cur[seg] = val
			return
		}
		nxt, ok := cur[seg].(map[string]any)
		if !ok {
			nxt = map[string]any{}
			cur[seg] = nxt
		}
		cur = nxt
	}
}

// isEmpty: missing, nil, blank string, empty slice / map.
func isEmpty(v any, present bool) bool {
	if !present || v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() == 0
	case reflect.Pointer:
		return rv.IsNil()
	}
	return false
}

// ---------------------------------------------------------------------------
// Running

// implicit rules run even when the value is empty.
var implicitRules = map[string]bool{
	"required": true, "required_if": true, "required_unless": true, "required_with": true,
	"required_with_all": true, "required_without": true, "required_without_all": true,
	"present": true, "filled": true, "accepted": true, "declined": true, "prohibited": true,
	"prohibited_if": true, "prohibited_unless": true, "custom": true,
}

func (v *RulesValidator) run() {
	if v.ran {
		return
	}
	v.ran = true
	v.validated = map[string]any{}

	fields := make([]string, 0, len(v.rules))
	for f := range v.rules {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	for _, pattern := range fields {
		rules, err := parseRules(v.rules[pattern])
		if err != nil {
			v.add(pattern, err.Error())
			continue
		}
		for _, path := range expand(v.data, pattern) {
			v.validateField(path, rules)
		}
	}
}

func (v *RulesValidator) validateField(path string, rules []parsedRule) {
	val, present := get(v.data, path)
	if hasRule(rules, "sometimes") && !present {
		return
	}
	empty := isEmpty(val, present)
	numeric := hasRule(rules, "numeric", "integer", "decimal")
	bail := hasRule(rules, "bail")
	failed := false
	for _, r := range rules {
		switch r.name {
		case "bail", "sometimes", "nullable":
			continue
		}
		if empty && !implicitRules[r.name] {
			continue
		}
		ok, msgParams := v.check(path, val, present, r, numeric)
		if ok {
			continue
		}
		failed = true
		v.add(path, v.message(path, r, val, numeric, msgParams))
		if bail {
			break
		}
	}
	if !failed && present {
		set(v.validated, path, val)
	}
}

func (v *RulesValidator) add(field, msg string) {
	v.errors[field] = append(v.errors[field], msg)
}

// check returns whether the rule passes, plus extra message parameters.
func (v *RulesValidator) check(path string, val any, present bool, r parsedRule, numeric bool) (bool, map[string]any) {
	p := r.params
	arg := func(i int) string {
		if i < len(p) {
			return strings.TrimSpace(p[i])
		}
		return ""
	}
	other := func(field string) (any, bool) { return get(v.data, field) }
	otherEmpty := func(field string) bool { o, ok := other(field); return isEmpty(o, ok) }
	equalsAny := func(o any, values []string) bool {
		s := fmt.Sprint(o)
		if b, ok := o.(bool); ok {
			s = strconv.FormatBool(b)
		}
		for _, x := range values {
			if s == strings.TrimSpace(x) {
				return true
			}
		}
		return false
	}
	empty := isEmpty(val, present)

	switch r.name {
	// --- presence
	case "required":
		return !empty, nil
	case "filled":
		return !present || !empty, nil
	case "present":
		return present, nil
	case "required_if":
		o, _ := other(arg(0))
		if equalsAny(o, p[1:]) {
			return !empty, map[string]any{"other": v.attr(arg(0)), "value": strings.Join(p[1:], ", ")}
		}
		return true, nil
	case "required_unless":
		o, _ := other(arg(0))
		if !equalsAny(o, p[1:]) {
			return !empty, map[string]any{"other": v.attr(arg(0)), "values": strings.Join(p[1:], ", ")}
		}
		return true, nil
	case "required_with", "required_with_all", "required_without", "required_without_all":
		count := 0
		for _, f := range p {
			if !otherEmpty(strings.TrimSpace(f)) {
				count++
			}
		}
		need := false
		switch r.name {
		case "required_with":
			need = count > 0
		case "required_with_all":
			need = count == len(p)
		case "required_without":
			need = count < len(p)
		case "required_without_all":
			need = count == 0
		}
		names := make([]string, len(p))
		for i, f := range p {
			names[i] = v.attr(strings.TrimSpace(f))
		}
		return !need || !empty, map[string]any{"values": strings.Join(names, " / ")}
	case "prohibited":
		return empty, nil
	case "prohibited_if":
		o, _ := other(arg(0))
		return !equalsAny(o, p[1:]) || empty, map[string]any{"other": v.attr(arg(0)), "value": strings.Join(p[1:], ", ")}
	case "prohibited_unless":
		o, _ := other(arg(0))
		return equalsAny(o, p[1:]) || empty, map[string]any{"other": v.attr(arg(0)), "values": strings.Join(p[1:], ", ")}
	case "accepted":
		return equalsAny(val, []string{"yes", "on", "1", "true"}), nil
	case "declined":
		return equalsAny(val, []string{"no", "off", "0", "false"}), nil

	// --- types
	case "string":
		_, ok := val.(string)
		return ok, nil
	case "integer":
		return isInteger(val), nil
	case "numeric":
		_, ok := toFloat(val)
		return ok, nil
	case "decimal":
		s, ok := numberString(val)
		if !ok {
			return false, nil
		}
		places := 0
		if _, frac, has := strings.Cut(s, "."); has {
			places = len(frac)
		}
		lo, _ := strconv.Atoi(arg(0))
		hi := lo
		if len(p) > 1 {
			hi, _ = strconv.Atoi(arg(1))
		}
		return places >= lo && places <= hi, map[string]any{"decimal": strings.Join(p, "-")}
	case "boolean":
		switch x := val.(type) {
		case bool:
			return true, nil
		default:
			return equalsAny(x, []string{"0", "1", "true", "false"}), nil
		}
	case "array":
		if !isList(val) && !isMap(val) {
			return false, nil
		}
		if len(p) > 0 && isMap(val) {
			allowed := map[string]bool{}
			for _, k := range p {
				allowed[strings.TrimSpace(k)] = true
			}
			for k := range val.(map[string]any) {
				if !allowed[k] {
					return false, nil
				}
			}
		}
		return true, nil
	case "list":
		return isList(val), nil
	case "json":
		s, ok := val.(string)
		return ok && json.Valid([]byte(s)), nil
	case "email":
		s, ok := val.(string)
		if !ok {
			return false, nil
		}
		a, err := mail.ParseAddress(s)
		return err == nil && a.Address == s && strings.Contains(s[strings.LastIndex(s, "@")+1:], "."), nil
	case "url", "active_url":
		s, ok := val.(string)
		if !ok {
			return false, nil
		}
		u, err := url.Parse(s)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && len(p) == 0) {
			return false, nil
		}
		if len(p) > 0 && !equalsAny(u.Scheme, p) {
			return false, nil
		}
		return true, nil
	case "uuid":
		s, ok := val.(string)
		return ok && uuidRe.MatchString(s), nil
	case "ulid":
		s, ok := val.(string)
		return ok && ulidRe.MatchString(s), nil
	case "ip":
		s, _ := val.(string)
		return net.ParseIP(s) != nil, nil
	case "ipv4":
		s, _ := val.(string)
		ip := net.ParseIP(s)
		return ip != nil && ip.To4() != nil && !strings.Contains(s, ":"), nil
	case "ipv6":
		s, _ := val.(string)
		ip := net.ParseIP(s)
		return ip != nil && strings.Contains(s, ":"), nil
	case "alpha", "alpha_num", "alpha_dash":
		s, ok := val.(string)
		if !ok {
			return false, nil
		}
		for _, c := range s {
			switch {
			case unicode.IsLetter(c):
			case r.name != "alpha" && unicode.IsDigit(c):
			case r.name == "alpha_dash" && (c == '-' || c == '_'):
			default:
				return false, nil
			}
		}
		return true, nil
	case "lowercase":
		s, ok := val.(string)
		return ok && s == strings.ToLower(s), nil
	case "uppercase":
		s, ok := val.(string)
		return ok && s == strings.ToUpper(s), nil
	case "regex", "not_regex":
		s, ok := val.(string)
		re, err := compileRegex(arg(0))
		if !ok || err != nil {
			return false, nil
		}
		return re.MatchString(s) == (r.name == "regex"), nil
	case "starts_with", "ends_with", "doesnt_start_with", "doesnt_end_with":
		s := fmt.Sprint(val)
		match := false
		for _, x := range p {
			if (strings.HasSuffix(r.name, "start_with") || r.name == "starts_with") && strings.HasPrefix(s, x) ||
				(strings.HasSuffix(r.name, "end_with") || r.name == "ends_with") && strings.HasSuffix(s, x) {
				match = true
			}
		}
		if strings.HasPrefix(r.name, "doesnt") {
			match = !match
		}
		return match, map[string]any{"values": strings.Join(p, ", ")}

	// --- size
	case "min", "max", "size", "between":
		size, ok := sizeOf(val, numeric)
		if !ok {
			return false, nil
		}
		a, _ := strconv.ParseFloat(arg(0), 64)
		switch r.name {
		case "min":
			return size >= a, map[string]any{"min": arg(0)}
		case "max":
			return size <= a, map[string]any{"max": arg(0)}
		case "size":
			return size == a, map[string]any{"size": arg(0)}
		default:
			b, _ := strconv.ParseFloat(arg(1), 64)
			return size >= a && size <= b, map[string]any{"min": arg(0), "max": arg(1)}
		}
	case "gt", "gte", "lt", "lte":
		size, ok := sizeOf(val, numeric)
		if !ok {
			return false, nil
		}
		limit, err := strconv.ParseFloat(arg(0), 64)
		if err != nil {
			o, ok := other(arg(0))
			if !ok {
				return false, nil
			}
			if limit, ok = sizeOf(o, numeric); !ok {
				return false, nil
			}
		}
		params := map[string]any{"value": strconv.FormatFloat(limit, 'f', -1, 64)}
		switch r.name {
		case "gt":
			return size > limit, params
		case "gte":
			return size >= limit, params
		case "lt":
			return size < limit, params
		default:
			return size <= limit, params
		}
	case "digits", "digits_between":
		s, ok := numberString(val)
		if !ok || strings.ContainsAny(s, ".-+eE") {
			return false, nil
		}
		n := len(s)
		lo, _ := strconv.Atoi(arg(0))
		if r.name == "digits" {
			return n == lo, map[string]any{"digits": arg(0)}
		}
		hi, _ := strconv.Atoi(arg(1))
		return n >= lo && n <= hi, map[string]any{"min": arg(0), "max": arg(1)}
	case "multiple_of":
		f, ok := toFloat(val)
		m, err := strconv.ParseFloat(arg(0), 64)
		if !ok || err != nil || m == 0 {
			return false, nil
		}
		q := f / m
		return q == float64(int64(q)), map[string]any{"value": arg(0)}

	// --- comparison
	case "same", "different":
		o, _ := other(arg(0))
		eq := fmt.Sprint(o) == fmt.Sprint(val)
		return eq == (r.name == "same"), map[string]any{"other": v.attr(arg(0))}
	case "confirmed":
		o, _ := other(path + "_confirmation")
		return fmt.Sprint(o) == fmt.Sprint(val), nil
	case "in", "not_in":
		in := false
		if isList(val) {
			in = true
			for _, e := range toList(val) {
				if !equalsAny(e, p) {
					in = false
				}
			}
		} else {
			in = equalsAny(val, p)
		}
		return in == (r.name == "in"), nil
	case "distinct":
		seen := map[string]bool{}
		for _, e := range toList(val) {
			k := fmt.Sprint(e)
			if seen[k] {
				return false, nil
			}
			seen[k] = true
		}
		return true, nil

	// --- dates
	case "date":
		_, ok := toTime(val, "")
		return ok, nil
	case "date_format":
		s, ok := val.(string)
		if !ok {
			return false, nil
		}
		_, err := time.Parse(arg(0), s)
		return err == nil, map[string]any{"format": arg(0)}
	case "before", "after", "before_or_equal", "after_or_equal", "date_equals":
		t, ok := toTime(val, "")
		if !ok {
			return false, nil
		}
		ref, ok := v.refTime(arg(0))
		if !ok {
			return false, nil
		}
		params := map[string]any{"date": arg(0)}
		switch r.name {
		case "before":
			return t.Before(ref), params
		case "after":
			return t.After(ref), params
		case "before_or_equal":
			return !t.After(ref), params
		case "after_or_equal":
			return !t.Before(ref), params
		default:
			return t.Equal(ref), params
		}
	case "timezone":
		s, _ := val.(string)
		_, err := time.LoadLocation(s)
		return s != "" && err == nil, nil

	// --- files
	case "file":
		return len(files(val)) > 0, nil
	case "image", "mimes", "mimetypes", "extensions":
		fs := files(val)
		if len(fs) == 0 {
			return false, nil
		}
		for _, f := range fs {
			if !fileMatches(f, r.name, p) {
				return false, map[string]any{"values": strings.Join(p, ", ")}
			}
		}
		return true, map[string]any{"values": strings.Join(p, ", ")}

	// --- database
	case "unique", "exists":
		if v.db == nil {
			return false, map[string]any{"error": "no database checker configured (Validator.DB)"}
		}
		table := arg(0)
		column := arg(1)
		if column == "" {
			column = lastSegment(path)
		}
		ignoreVal, ignoreCol := "", ""
		if r.name == "unique" && arg(2) != "" && !strings.EqualFold(arg(2), "null") {
			ignoreVal = arg(2)
			ignoreCol = arg(3)
			if ignoreCol == "" {
				ignoreCol = "id"
			}
		}
		var ignore any
		if ignoreCol != "" {
			ignore = ignoreVal
		}
		values := []any{val}
		if r.name == "exists" && isList(val) {
			values = toList(val)
		}
		for _, x := range values {
			n, err := v.db.Count(v.ctx, table, column, x, ignoreCol, ignore)
			if err != nil {
				return false, nil
			}
			if (r.name == "unique" && n > 0) || (r.name == "exists" && n == 0) {
				return false, nil
			}
		}
		return true, nil

	case "custom":
		return r.custom.Passes(v.ctx, path, val, v.data), nil
	}
	return false, map[string]any{"unknown": r.name}
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

// refTime resolves a date rule argument: today / tomorrow / yesterday /
// now, another field, or a date literal.
func (v *RulesValidator) refTime(arg string) (time.Time, bool) {
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch strings.ToLower(arg) {
	case "now":
		return now, true
	case "today":
		return day, true
	case "tomorrow":
		return day.AddDate(0, 0, 1), true
	case "yesterday":
		return day.AddDate(0, 0, -1), true
	}
	if o, ok := get(v.data, arg); ok {
		return toTime(o, "")
	}
	return toTime(arg, "")
}

// ---------------------------------------------------------------------------
// Value helpers

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	ulidRe     = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{25}$`)
	regexCache sync.Map
)

// compileRegex accepts Go syntax, optionally wrapped in /.../ (PHP style).
func compileRegex(pattern string) (*regexp.Regexp, error) {
	if c, ok := regexCache.Load(pattern); ok {
		return c.(*regexp.Regexp), nil
	}
	expr := pattern
	if len(expr) >= 2 && expr[0] == '/' {
		if end := strings.LastIndex(expr, "/"); end > 0 {
			flags := expr[end+1:]
			expr = expr[1:end]
			if strings.Contains(flags, "i") {
				expr = "(?i)" + expr
			}
		}
	}
	re, err := regexp.Compile(expr)
	if err == nil {
		regexCache.Store(pattern, re)
	}
	return re, err
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func isInteger(v any) bool {
	switch x := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return x == float64(int64(x))
	case float32:
		return x == float32(int64(x))
	case json.Number:
		_, err := x.Int64()
		return err == nil
	case string:
		_, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return err == nil
	}
	return false
}

func numberString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		_, err := strconv.ParseFloat(s, 64)
		return s, err == nil
	case json.Number:
		return x.String(), true
	}
	if f, ok := toFloat(v); ok {
		return strconv.FormatFloat(f, 'f', -1, 64), true
	}
	return "", false
}

func isList(v any) bool {
	if _, ok := v.([]any); ok {
		return true
	}
	if _, ok := v.([]*multipart.FileHeader); ok {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Type().Elem().Kind() != reflect.Uint8
}

func isMap(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

func toList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out
}

// sizeOf: numbers (when the field is numeric), item counts, file size in
// kilobytes, or string length in characters.
func sizeOf(v any, numeric bool) (float64, bool) {
	if fs := files(v); len(fs) == 1 {
		return float64(fs[0].Size) / 1024, true
	}
	if numeric {
		return toFloat(v)
	}
	switch x := v.(type) {
	case string:
		return float64(utf8.RuneCountInString(x)), true
	case map[string]any:
		return float64(len(x)), true
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return toFloat(x)
	}
	if isList(v) {
		return float64(len(toList(v))), true
	}
	return 0, false
}

var dateLayouts = []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"}

func toTime(v any, layout string) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, !x.IsZero()
	case *time.Time:
		if x != nil {
			return *x, true
		}
	case string:
		s := strings.TrimSpace(x)
		if layout != "" {
			t, err := time.Parse(layout, s)
			return t, err == nil
		}
		for _, l := range dateLayouts {
			if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// ---------------------------------------------------------------------------
// Messages

var defaultMessages = map[string]string{
	"required":             "The :attribute field is required.",
	"filled":               "The :attribute field must have a value.",
	"present":              "The :attribute field must be present.",
	"required_if":          "The :attribute field is required when :other is :value.",
	"required_unless":      "The :attribute field is required unless :other is in :values.",
	"required_with":        "The :attribute field is required when :values is present.",
	"required_with_all":    "The :attribute field is required when :values are present.",
	"required_without":     "The :attribute field is required when :values is not present.",
	"required_without_all": "The :attribute field is required when none of :values are present.",
	"prohibited":           "The :attribute field is prohibited.",
	"prohibited_if":        "The :attribute field is prohibited when :other is :value.",
	"prohibited_unless":    "The :attribute field is prohibited unless :other is in :values.",
	"accepted":             "The :attribute field must be accepted.",
	"declined":             "The :attribute field must be declined.",
	"string":               "The :attribute field must be a string.",
	"integer":              "The :attribute field must be an integer.",
	"numeric":              "The :attribute field must be a number.",
	"decimal":              "The :attribute field must have :decimal decimal places.",
	"boolean":              "The :attribute field must be true or false.",
	"array":                "The :attribute field must be an array.",
	"list":                 "The :attribute field must be a list.",
	"json":                 "The :attribute field must be a valid JSON string.",
	"email":                "The :attribute field must be a valid email address.",
	"url":                  "The :attribute field must be a valid URL.",
	"active_url":           "The :attribute field must be a valid URL.",
	"uuid":                 "The :attribute field must be a valid UUID.",
	"ulid":                 "The :attribute field must be a valid ULID.",
	"ip":                   "The :attribute field must be a valid IP address.",
	"ipv4":                 "The :attribute field must be a valid IPv4 address.",
	"ipv6":                 "The :attribute field must be a valid IPv6 address.",
	"alpha":                "The :attribute field must only contain letters.",
	"alpha_num":            "The :attribute field must only contain letters and numbers.",
	"alpha_dash":           "The :attribute field must only contain letters, numbers, dashes, and underscores.",
	"lowercase":            "The :attribute field must be lowercase.",
	"uppercase":            "The :attribute field must be uppercase.",
	"regex":                "The :attribute field format is invalid.",
	"not_regex":            "The :attribute field format is invalid.",
	"starts_with":          "The :attribute field must start with one of the following: :values.",
	"ends_with":            "The :attribute field must end with one of the following: :values.",
	"doesnt_start_with":    "The :attribute field must not start with one of the following: :values.",
	"doesnt_end_with":      "The :attribute field must not end with one of the following: :values.",
	"gt":                   "The :attribute field must be greater than :value.",
	"gte":                  "The :attribute field must be greater than or equal to :value.",
	"lt":                   "The :attribute field must be less than :value.",
	"lte":                  "The :attribute field must be less than or equal to :value.",
	"digits":               "The :attribute field must be :digits digits.",
	"digits_between":       "The :attribute field must be between :min and :max digits.",
	"multiple_of":          "The :attribute field must be a multiple of :value.",
	"same":                 "The :attribute field must match :other.",
	"different":            "The :attribute field and :other must be different.",
	"confirmed":            "The :attribute field confirmation does not match.",
	"in":                   "The selected :attribute is invalid.",
	"not_in":               "The selected :attribute is invalid.",
	"distinct":             "The :attribute field has a duplicate value.",
	"date":                 "The :attribute field must be a valid date.",
	"date_format":          "The :attribute field must match the format :format.",
	"before":               "The :attribute field must be a date before :date.",
	"after":                "The :attribute field must be a date after :date.",
	"before_or_equal":      "The :attribute field must be a date before or equal to :date.",
	"after_or_equal":       "The :attribute field must be a date after or equal to :date.",
	"date_equals":          "The :attribute field must be a date equal to :date.",
	"timezone":             "The :attribute field must be a valid timezone.",
	"file":                 "The :attribute field must be a file.",
	"image":                "The :attribute field must be an image.",
	"mimes":                "The :attribute field must be a file of type: :values.",
	"mimetypes":            "The :attribute field must be a file of type: :values.",
	"extensions":           "The :attribute field must have one of the following extensions: :values.",
	"unique":               "The :attribute has already been taken.",
	"exists":               "The selected :attribute is invalid.",
}

// sizeMessages depend on what is being measured.
var sizeMessages = map[string]map[string]string{
	"min":     {"numeric": "The :attribute field must be at least :min.", "file": "The :attribute field must be at least :min kilobytes.", "string": "The :attribute field must be at least :min characters.", "array": "The :attribute field must have at least :min items."},
	"max":     {"numeric": "The :attribute field must not be greater than :max.", "file": "The :attribute field must not be greater than :max kilobytes.", "string": "The :attribute field must not be greater than :max characters.", "array": "The :attribute field must not have more than :max items."},
	"size":    {"numeric": "The :attribute field must be :size.", "file": "The :attribute field must be :size kilobytes.", "string": "The :attribute field must be :size characters.", "array": "The :attribute field must contain :size items."},
	"between": {"numeric": "The :attribute field must be between :min and :max.", "file": "The :attribute field must be between :min and :max kilobytes.", "string": "The :attribute field must be between :min and :max characters.", "array": "The :attribute field must have between :min and :max items."},
}

func sizeKind(v any, numeric bool) string {
	switch {
	case len(files(v)) == 1:
		return "file"
	case numeric:
		return "numeric"
	}
	if _, ok := v.(string); ok {
		return "string"
	}
	if isList(v) || isMap(v) {
		return "array"
	}
	if _, ok := toFloat(v); ok {
		return "numeric"
	}
	return "string"
}

// attr is the display name of a field: Attributes, else the last path
// segment(s) with underscores as spaces ("items.0.unit_price" -> "items.0.unit price").
func (v *RulesValidator) attr(field string) string {
	if a, ok := v.attributes[field]; ok {
		return a
	}
	// "items.*.name" style overrides
	for k, a := range v.attributes {
		if strings.Contains(k, "*") {
			if re, err := regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(k), `\*`, `[^.]+`) + "$"); err == nil && re.MatchString(field) {
				return a
			}
		}
	}
	return strings.ReplaceAll(field, "_", " ")
}

func (v *RulesValidator) message(path string, r parsedRule, val any, numeric bool, params map[string]any) string {
	if params == nil {
		params = map[string]any{}
	}
	params["attribute"] = v.attr(path)
	if r.name == "custom" {
		return interpolate(r.custom.Message(), params)
	}
	if u, ok := params["unknown"]; ok {
		return fmt.Sprintf("validation: unknown rule %q on %s", u, path)
	}
	if e, ok := params["error"]; ok {
		return fmt.Sprintf("validation: %s on %s", e, path)
	}
	// Custom messages: field.rule, wildcard field.rule, field, rule.
	pattern := v.patternFor(path)
	for _, k := range []string{path + "." + r.name, pattern + "." + r.name, path, pattern, r.name} {
		if m, ok := v.messages[k]; ok {
			return interpolate(m, params)
		}
	}
	key := r.name
	line := defaultMessages[r.name]
	if m, ok := sizeMessages[r.name]; ok {
		kind := sizeKind(val, numeric)
		line = m[kind]
		key = r.name + "." + kind
	}
	if v.translate != nil {
		if t := v.translate("validation."+key, params); t != "" && t != "validation."+key {
			return t
		}
	}
	if line == "" {
		line = "The :attribute field is invalid."
	}
	return interpolate(line, params)
}

// patternFor returns the rule pattern a concrete path came from.
func (v *RulesValidator) patternFor(path string) string {
	for k := range v.rules {
		if strings.Contains(k, "*") {
			if re, err := regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(k), `\*`, `[^.]+`) + "$"); err == nil && re.MatchString(path) {
				return k
			}
		}
	}
	return path
}

// interpolate replaces :name placeholders, longest names first so :min
// does not eat part of :minutes.
func interpolate(line string, params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		line = strings.ReplaceAll(line, ":"+k, fmt.Sprint(params[k]))
	}
	return line
}
