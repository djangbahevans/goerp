package domain

import "fmt"

// Parse parses a domain expression string into an Expr AST. Operator
// precedence, highest to lowest binding power, follows manifest-spec.md §8
// "Domain expression operators" exactly: NOT, comparison, LIKE/ILIKE,
// IS NULL, IN, child_of/parent_of, AND, OR.
func Parse(src string) (Expr, error) {
	toks, err := newLexer(src).tokenize()
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tokEOF {
		return nil, fmt.Errorf("domain: unexpected token %q at position %d", p.cur().text, p.cur().pos)
	}
	return expr, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) cur() token { return p.toks[p.pos] }
func (p *parser) advance() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) expect(kind tokenKind, what string) (token, error) {
	if p.cur().kind != kind {
		return token{}, fmt.Errorf("domain: expected %s at position %d, got %q", what, p.cur().pos, p.cur().text)
	}
	return p.advance(), nil
}

// OR — lowest precedence.
func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokOr {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = BinaryExpr{Op: "OR", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseChildOf()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokAnd {
		p.advance()
		right, err := p.parseChildOf()
		if err != nil {
			return nil, err
		}
		left = BinaryExpr{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseChildOf() (Expr, error) {
	left, err := p.parseIn()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokChildOf || p.cur().kind == tokParentOf {
		if !isBareRecord(left) {
			return nil, fmt.Errorf("domain: %s must follow the bare `record` keyword at position %d", p.cur().text, p.cur().pos)
		}
		op := "child_of"
		if p.cur().kind == tokParentOf {
			op = "parent_of"
		}
		p.advance()
		target, err := p.parseIn()
		if err != nil {
			return nil, err
		}
		left = TreeExpr{Op: op, Target: target}
	}
	return left, nil
}

// isBareRecord reports whether expr is the literal `record` keyword used
// on its own (not `record.field`) — the only valid LHS of child_of/parent_of.
func isBareRecord(expr Expr) bool {
	rf, ok := expr.(RecordField)
	return ok && rf.Field == ""
}

func (p *parser) parseIn() (Expr, error) {
	left, err := p.parseIsNull()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokIn {
		p.advance()
		if _, err := p.expect(tokLParen, "'('"); err != nil {
			return nil, err
		}
		var values []Expr
		for {
			v, err := p.parseIsNull()
			if err != nil {
				return nil, err
			}
			values = append(values, v)
			if p.cur().kind == tokComma {
				p.advance()
				continue
			}
			break
		}
		if _, err := p.expect(tokRParen, "')'"); err != nil {
			return nil, err
		}
		left = InExpr{Operand: left, Values: values}
	}
	return left, nil
}

func (p *parser) parseIsNull() (Expr, error) {
	left, err := p.parseLikeIlike()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokIs {
		p.advance()
		not := false
		if p.cur().kind == tokNot {
			not = true
			p.advance()
		}
		if _, err := p.expect(tokNull, "'NULL'"); err != nil {
			return nil, err
		}
		left = IsNullExpr{Operand: left, Not: not}
	}
	return left, nil
}

func (p *parser) parseLikeIlike() (Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokLike || p.cur().kind == tokIlike {
		op := "LIKE"
		if p.cur().kind == tokIlike {
			op = "ILIKE"
		}
		p.advance()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

var comparisonOps = map[tokenKind]string{
	tokEq:  "=",
	tokNeq: "!=",
	tokLt:  "<",
	tokGt:  ">",
	tokLte: "<=",
	tokGte: ">=",
}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		op, ok := comparisonOps[p.cur().kind]
		if !ok {
			return left, nil
		}
		p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = BinaryExpr{Op: op, Left: left, Right: right}
	}
}

// NOT — highest precedence, unary prefix.
func (p *parser) parseUnary() (Expr, error) {
	if p.cur().kind == tokNot {
		p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return UnaryExpr{Op: "NOT", Operand: operand}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	tok := p.cur()
	switch tok.kind {
	case tokLParen:
		p.advance()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen, "')'"); err != nil {
			return nil, err
		}
		return expr, nil
	case tokTrue:
		p.advance()
		return Literal{Value: true}, nil
	case tokFalse:
		p.advance()
		return Literal{Value: false}, nil
	case tokNull:
		p.advance()
		return Literal{Value: nil}, nil
	case tokString:
		p.advance()
		return Literal{Value: tok.text}, nil
	case tokNumber:
		p.advance()
		return Literal{Value: Number(tok.text)}, nil
	case tokIdent:
		return p.parseIdentExpr()
	default:
		return nil, fmt.Errorf("domain: unexpected token %q at position %d", tok.text, tok.pos)
	}
}

func (p *parser) parseIdentExpr() (Expr, error) {
	name := p.advance().text

	switch name {
	case "record":
		if p.cur().kind == tokDot {
			p.advance()
			field, err := p.expect(tokIdent, "field name")
			if err != nil {
				return nil, err
			}
			return RecordField{Field: field.text}, nil
		}
		// Bare `record` is only valid immediately before child_of/parent_of;
		// parseChildOf enforces that once it sees this sentinel value.
		if p.cur().kind != tokChildOf && p.cur().kind != tokParentOf {
			return nil, fmt.Errorf("domain: bare `record` must be followed by child_of/parent_of at position %d, got %q", p.cur().pos, p.cur().text)
		}
		return RecordField{Field: ""}, nil
	case "current_user", "user":
		if _, err := p.expect(tokDot, "'.'"); err != nil {
			return nil, err
		}
		attr, err := p.expect(tokIdent, "attribute name")
		if err != nil {
			return nil, err
		}
		return UserAttr{Attr: attr.text}, nil
	case "tenant":
		if _, err := p.expect(tokDot, "'.'"); err != nil {
			return nil, err
		}
		field, err := p.expect(tokIdent, "field name")
		if err != nil {
			return nil, err
		}
		return TenantAttr{Field: field.text}, nil
	case "user_has_role":
		return p.parseCall(func(arg string) Expr { return RoleCheck{Role: arg} })
	case "user_has_permission":
		return p.parseCall(func(arg string) Expr { return PermCheck{Perm: arg} })
	default:
		return nil, fmt.Errorf("domain: unknown identifier %q at position %d", name, p.toks[p.pos-1].pos)
	}
}

func (p *parser) parseCall(build func(string) Expr) (Expr, error) {
	if _, err := p.expect(tokLParen, "'('"); err != nil {
		return nil, err
	}
	arg, err := p.expect(tokString, "string literal argument")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokRParen, "')'"); err != nil {
		return nil, err
	}
	return build(arg.text), nil
}
