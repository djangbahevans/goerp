package transfer

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

type RelationSummary struct {
	Module string `json:"module"`
	Model  string `json:"model"`
	Field  string `json:"field"`
	Count  int64  `json:"count"`
}

type RelationOmission struct {
	Module         string   `json:"module"`
	Model          string   `json:"model"`
	Field          string   `json:"field"`
	MissingModules []string `json:"missing_modules"`
}

type Relation struct {
	Module       string
	Model        string
	Field        string
	SourceModule string
	TargetModule string
	Table        string
	Column1      string
	Column2      string
}

type Link struct {
	Model    string `json:"model"`
	Field    string `json:"field"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

type LinkRecord struct {
	Relation *Link `json:"relation,omitempty"`
}

func Relations(sourceModule string, models []model.ModelDeclaration) []Relation {
	var relations []Relation
	for _, md := range models {
		if md.Backend != "" {
			continue
		}

		for _, field := range md.Fields {
			def := field.Def
			if def.Kind != model.KindMany2Many || def.ReadPermission != "" {
				continue
			}

			targetModule, _, _ := strings.Cut(def.RelatedModel, ".")
			relations = append(relations, Relation{
				Module:       cmp.Or(field.DeclaringModule, sourceModule),
				Model:        md.QualifiedName(sourceModule),
				Field:        field.Name,
				SourceModule: sourceModule,
				TargetModule: targetModule,
				Table:        def.JoinTable,
				Column1:      def.Column1,
				Column2:      def.Column2,
			})
		}
	}
	return relations
}

func SelectRelations(relations []Relation, modules []string) ([]Relation, []RelationOmission) {
	var included []Relation
	var omitted []RelationOmission
	for _, relation := range relations {
		if !slices.Contains(modules, relation.Module) {
			continue
		}

		var missing []string
		for _, name := range []string{relation.SourceModule, relation.TargetModule} {
			if !slices.Contains(modules, name) && !slices.Contains(missing, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			omitted = append(omitted, RelationOmission{
				Module:         relation.Module,
				Model:          relation.Model,
				Field:          relation.Field,
				MissingModules: missing,
			})
			continue
		}

		included = append(included, relation)
	}
	return included, omitted
}

func SummarizeRelations(relations []Relation, data map[string][]byte) ([]RelationSummary, error) {
	summaries := make([]RelationSummary, len(relations))
	indices := make(map[string]int, len(relations))
	for i, relation := range relations {
		summaries[i] = RelationSummary{
			Module: relation.Module,
			Model:  relation.Model,
			Field:  relation.Field,
		}
		indices[relation.Module+":"+relation.Model+":"+relation.Field] = i
	}

	for module, content := range data {
		scanner := bufio.NewScanner(bytes.NewReader(content))
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var rec LinkRecord
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				return nil, fmt.Errorf("decode module %q relation: %w", module, err)
			}
			if rec.Relation == nil {
				continue
			}

			index, ok := indices[module+":"+rec.Relation.Model+":"+rec.Relation.Field]
			if !ok {
				return nil, fmt.Errorf("unknown relation %s.%s in module %q", rec.Relation.Model, rec.Relation.Field, module)
			}
			summaries[index].Count++
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("scan module %q relations: %w", module, err)
		}
	}
	return summaries, nil
}
