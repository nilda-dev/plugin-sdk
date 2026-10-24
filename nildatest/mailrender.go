package nildatest

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// A small reading of the Liquid subset Core's mail templates are written in — `{{ x.y | default: "…" }}`,
// `{% if %}/{% elsif %}/{% else %}`, `{% for x in list limit: n %}`, `{% assign %}` — so a plugin's tests see the
// words its declared defaults produce. Core's own engine is the authority on a real site: it also bounds every
// render and refuses what does not parse when the manifest is installed (`nilda plugin check` runs Core's check).

// safeHTML is a value printed as it is in an HTML part: a variable of kind html.
type safeHTML string

type mailNode struct {
	kind     string // text, out, if, for, assign
	text     string
	expr     string
	branches []mailBranch // if: conditions and bodies; the last may have cond "" (else)
	loopVar  string
	listExpr string
	limit    int
	body     []mailNode
}

type mailBranch struct {
	cond string
	body []mailNode
}

var mailToken = regexp.MustCompile(`(?s)\{\{-?(.*?)-?\}\}|\{%-?(.*?)-?%\}`)

// renderMailTemplate renders src for vars; html escapes every value but a safeHTML one.
func renderMailTemplate(src string, vars map[string]any, htmlPart bool) (string, error) {
	nodes, rest, err := parseMailNodes(&mailLexer{src: src}, nil)
	if err != nil {
		return "", err
	}
	if rest != "" {
		return "", fmt.Errorf("{%% %s %%} closes nothing", rest)
	}
	var b strings.Builder
	scope := []map[string]any{vars, {}}
	if err := runMailNodes(&b, nodes, scope, htmlPart); err != nil {
		return "", err
	}
	return b.String(), nil
}

type mailLexer struct {
	src string
	pos int
}

// next returns the next piece: text, an output expression, or a tag.
func (l *mailLexer) next() (kind, body string, ok bool) {
	if l.pos >= len(l.src) {
		return "", "", false
	}
	loc := mailToken.FindStringSubmatchIndex(l.src[l.pos:])
	if loc == nil {
		body = l.src[l.pos:]
		l.pos = len(l.src)
		return "text", body, true
	}
	if loc[0] > 0 {
		body = l.src[l.pos : l.pos+loc[0]]
		l.pos += loc[0]
		return "text", body, true
	}
	m := l.src[l.pos : l.pos+loc[1]]
	l.pos += loc[1]
	if strings.HasPrefix(m, "{{") {
		return "out", strings.TrimSpace(strings.Trim(m[2:len(m)-2], "-")), true
	}
	return "tag", strings.TrimSpace(strings.Trim(m[2:len(m)-2], "-")), true
}

// parseMailNodes reads nodes until one of stops (a tag name), returning which stopped it.
func parseMailNodes(l *mailLexer, stops map[string]bool) ([]mailNode, string, error) {
	var out []mailNode
	for {
		kind, body, ok := l.next()
		if !ok {
			if stops != nil {
				return nil, "", fmt.Errorf("a block is not closed")
			}
			return out, "", nil
		}
		switch kind {
		case "text":
			out = append(out, mailNode{kind: "text", text: body})
		case "out":
			out = append(out, mailNode{kind: "out", expr: body})
		case "tag":
			name, args, _ := strings.Cut(body, " ")
			args = strings.TrimSpace(args)
			if stops[name] {
				// The stop tag's arguments travel in the returned string ("elsif x").
				return out, strings.TrimSpace(name + " " + args), nil
			}
			switch name {
			case "if":
				n := mailNode{kind: "if"}
				cond := args
				for {
					body, stop, err := parseMailNodes(l, map[string]bool{"elsif": true, "else": true, "endif": true})
					if err != nil {
						return nil, "", err
					}
					n.branches = append(n.branches, mailBranch{cond: cond, body: body})
					sname, sargs, _ := strings.Cut(stop, " ")
					if sname == "endif" {
						break
					}
					if sname == "else" {
						cond = "true"
						continue
					}
					cond = sargs
				}
				out = append(out, n)
			case "for":
				m := regexp.MustCompile(`^(\w+)\s+in\s+([\w.\[\]]+)(?:\s+limit\s*:\s*(\d+))?$`).FindStringSubmatch(args)
				if m == nil {
					return nil, "", fmt.Errorf("{%% for %s %%} does not read as a loop", args)
				}
				body, _, err := parseMailNodes(l, map[string]bool{"endfor": true})
				if err != nil {
					return nil, "", err
				}
				limit, _ := strconv.Atoi(m[3])
				out = append(out, mailNode{kind: "for", loopVar: m[1], listExpr: m[2], limit: limit, body: body})
			case "assign":
				v, expr, ok := strings.Cut(args, "=")
				if !ok {
					return nil, "", fmt.Errorf("{%% assign %s %%} has no value", args)
				}
				out = append(out, mailNode{kind: "assign", loopVar: strings.TrimSpace(v), expr: strings.TrimSpace(expr)})
			default:
				return nil, "", fmt.Errorf("{%% %s %%} is not a tag Core's mail templates have", name)
			}
		}
	}
}

func runMailNodes(b *strings.Builder, nodes []mailNode, scope []map[string]any, htmlPart bool) error {
	for _, n := range nodes {
		switch n.kind {
		case "text":
			b.WriteString(n.text)
		case "out":
			v := evalMailExpr(n.expr, scope)
			if s, ok := v.(safeHTML); ok && htmlPart {
				b.WriteString(string(s))
			} else if htmlPart {
				b.WriteString(html.EscapeString(mailText(v)))
			} else {
				b.WriteString(mailText(v))
			}
		case "assign":
			scope[1][n.loopVar] = evalMailExpr(n.expr, scope)
		case "if":
			for _, br := range n.branches {
				if mailCond(br.cond, scope) {
					if err := runMailNodes(b, br.body, scope, htmlPart); err != nil {
						return err
					}
					break
				}
			}
		case "for":
			list, _ := evalMailExpr(n.listExpr, scope).([]any)
			for i, item := range list {
				if n.limit > 0 && i >= n.limit {
					break
				}
				inner := append(append([]map[string]any(nil), scope...), map[string]any{n.loopVar: item,
					"forloop": map[string]any{"index": i + 1, "first": i == 0, "last": i == len(list)-1}})
				if err := runMailNodes(b, n.body, inner, htmlPart); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func mailCond(cond string, scope []map[string]any) bool {
	for _, or := range strings.Split(cond, " or ") {
		all := true
		for _, and := range strings.Split(or, " and ") {
			if !mailCompare(strings.TrimSpace(and), scope) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func mailCompare(c string, scope []map[string]any) bool {
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		if l, r, ok := strings.Cut(c, " "+op+" "); ok {
			a, b := evalMailExpr(l, scope), evalMailExpr(r, scope)
			switch op {
			case "==":
				return mailText(a) == mailText(b)
			case "!=":
				return mailText(a) != mailText(b)
			}
			x, err1 := strconv.ParseFloat(mailText(a), 64)
			y, err2 := strconv.ParseFloat(mailText(b), 64)
			if err1 != nil || err2 != nil {
				return false
			}
			switch op {
			case ">=":
				return x >= y
			case "<=":
				return x <= y
			case ">":
				return x > y
			default:
				return x < y
			}
		}
	}
	return mailTruthy(evalMailExpr(c, scope))
}

// evalMailExpr is an operand with its filters.
func evalMailExpr(expr string, scope []map[string]any) any {
	parts := strings.Split(expr, "|")
	v := mailOperand(strings.TrimSpace(parts[0]), scope)
	for _, f := range parts[1:] {
		name, arg, _ := strings.Cut(strings.TrimSpace(f), ":")
		argv := mailOperand(strings.TrimSpace(arg), scope)
		switch strings.TrimSpace(name) {
		case "default":
			if !mailTruthy(v) {
				v = argv
			}
		case "upcase":
			v = strings.ToUpper(mailText(v))
		case "downcase":
			v = strings.ToLower(mailText(v))
		case "size":
			if l, ok := v.([]any); ok {
				v = len(l)
			} else {
				v = len([]rune(mailText(v)))
			}
		case "join":
			if l, ok := v.([]any); ok {
				s := make([]string, len(l))
				for i, e := range l {
					s[i] = mailText(e)
				}
				v = strings.Join(s, mailText(argv))
			}
		}
	}
	return v
}

func mailOperand(s string, scope []map[string]any) any {
	switch {
	case s == "":
		return nil
	case s == "true":
		return true
	case s == "false":
		return false
	case strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2,
		strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2:
		return s[1 : len(s)-1]
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	path := strings.Split(s, ".")
	var v any
	for i := len(scope) - 1; i >= 0; i-- {
		if x, ok := scope[i][path[0]]; ok {
			v = x
			break
		}
	}
	for _, k := range path[1:] {
		switch x := v.(type) {
		case map[string]any:
			if k == "size" {
				if e, ok := x[k]; ok {
					v = e
				} else {
					v = len(x)
				}
				continue
			}
			v = x[k]
		case []any:
			switch k {
			case "size":
				v = len(x)
			case "first":
				v = nil
				if len(x) > 0 {
					v = x[0]
				}
			case "last":
				v = nil
				if len(x) > 0 {
					v = x[len(x)-1]
				}
			default:
				v = nil
			}
		default:
			v = nil
		}
	}
	return v
}

func mailTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case safeHTML:
		return x != ""
	case []any:
		return len(x) > 0
	}
	return true
}

func mailText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case safeHTML:
		return string(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprint(v)
}
