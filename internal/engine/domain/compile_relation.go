package domain

import (
	"fmt"
	"strings"
	"uuid"
)

// SQLRelation describes a correlated join query. Its SQL fragments come from
// trusted model metadata; they must not contain user input or placeholders.
type SQLRelation struct {
	From   string
	Where  string
	Column string
}

func (r SQLRelation) exists(predicate string) string {
	return "EXISTS (SELECT 1 FROM " + r.From + " WHERE (" + r.Where + ") AND (" + predicate + "))"
}

func (c *sqlCompiler) relation(expr Expr) (SQLRelation, bool) {
	field, ok := expr.(RecordField)
	if !ok {
		return SQLRelation{}, false
	}
	rel, ok := c.relations[field.Field]
	return rel, ok
}

func (c *sqlCompiler) compileRelation(rel SQLRelation, values []Expr) (string, error) {
	if len(values) == 0 {
		return "false", nil
	}

	params := make([]string, len(values))
	for i, value := range values {
		literal, ok := value.(Literal)
		if !ok {
			return "", fmt.Errorf("domain: relation membership requires UUID literals")
		}
		id, ok := literal.Value.(string)
		if !ok {
			return "", fmt.Errorf("domain: relation membership requires UUID literals")
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			return "", fmt.Errorf("domain: invalid relation UUID %q", id)
		}
		params[i] = c.bindArg(parsed.String())
	}

	return rel.exists(rel.Column + " IN (" + strings.Join(params, ", ") + ")"), nil
}
