package main

import (
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"uuid"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/authz"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

const (
	duplicatePageSize = 200
	duplicateLimit    = 10
)

type duplicateCandidate struct {
	Type          models.ContactType `json:"type"`
	GivenName     string             `json:"given_name"`
	OtherNames    string             `json:"other_names"`
	FamilyName    string             `json:"family_name"`
	LegalName     string             `json:"legal_name"`
	Title         string             `json:"title"`
	PreferredName string             `json:"preferred_name"`
	TradingName   string             `json:"trading_name"`
	Phone         string             `json:"phone"`
	Email         string             `json:"email"`
	Identifiers   []struct {
		TypeID string `json:"type_id"`
		Value  string `json:"value"`
	} `json:"identifiers"`
	ExcludeID string `json:"exclude_id"`
}

type duplicateItem struct {
	ID          string             `json:"id"`
	DisplayName string             `json:"display_name"`
	Type        models.ContactType `json:"type"`
	Phone       *string            `json:"phone"`
	Email       *string            `json:"email"`
	Reasons     []string           `json:"reasons"`
}

func init() {
	engine.HandleAction(engine.DefineAction[models.Contact, duplicateCandidate]("duplicates",
		engine.Scope(engine.CollectionAction),
		engine.Method(engine.MethodPost),
		engine.Requires(schema.ContactRead),
	), duplicateContacts)
}

func duplicateContacts(_ *engine.Request, candidate duplicateCandidate) *engine.Response {
	if candidate.Type != "person" && candidate.Type != "organization" {
		return engine.BadRequest(errors.New("type must be person or organization"))
	}
	if candidate.ExcludeID != "" {
		id, err := uuid.Parse(candidate.ExcludeID)
		if err != nil {
			return engine.BadRequest(errors.New("exclude_id must be a UUID"))
		}
		candidate.ExcludeID = id.String()
	}
	for i := range candidate.Identifiers {
		id, err := uuid.Parse(candidate.Identifiers[i].TypeID)
		if err != nil {
			return engine.BadRequest(errors.New("identifier type_id must be a UUID"))
		}
		candidate.Identifiers[i].TypeID = id.String()
	}

	identifiers, err := candidate.readableIdentifiers()
	if err != nil {
		return duplicateFailure()
	}

	filter := models.ContactFields.IsActive.Eq(true)
	if candidate.ExcludeID != "" {
		filter = filter.And(models.ContactFields.ID.Eq(candidate.ExcludeID).Not())
	}
	query := orm.From[models.Contact]().Where(filter).Select(
		models.ContactFields.ID, models.ContactFields.DisplayName, models.ContactFields.Type,
		models.ContactFields.FullName, models.ContactFields.Phone, models.ContactFields.Email,
	).OrderBy(models.ContactFields.ID, false).Limit(duplicatePageSize)

	items := []duplicateItem{}
	for {
		contacts, cursor, err := query.All()
		if err != nil {
			return duplicateFailure()
		}
		reasons, err := candidate.matchPage(contacts, identifiers)
		if err != nil {
			return duplicateFailure()
		}
		for _, contact := range contacts {
			if len(reasons[contact.ID]) == 0 {
				continue
			}
			items = append(items, duplicateItem{
				ID:          contact.ID,
				DisplayName: contact.DisplayName,
				Type:        contact.Type,
				Phone:       contact.Phone,
				Email:       contact.Email,
				Reasons:     reasons[contact.ID],
			})
			if len(items) == duplicateLimit {
				return engine.OK(items)
			}
		}
		if cursor == "" {
			return engine.OK(items)
		}
		query.Cursor(cursor)
	}
}

func (candidate duplicateCandidate) readableIdentifiers() (map[string][]string, error) {
	values := make(map[string][]string)
	if len(candidate.Identifiers) == 0 {
		return values, nil
	}
	allowed, err := authz.Check(schema.IdentifierRead, "")
	if err != nil || !allowed {
		return values, err
	}

	personalRead, err := authz.Check(schema.PersonalRead, "")
	if err != nil {
		return nil, err
	}

	for _, identifier := range candidate.Identifiers {
		typ, err := orm.Get[models.IdentifierType](identifier.TypeID)
		if errors.Is(err, orm.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if typ.IsSensitive && !personalRead {
			continue
		}

		value := normalizeIdentifier(identifier.Value, typ)
		if value != "" {
			values[typ.ID] = append(values[typ.ID], value)
		}
	}
	return values, nil
}

func (candidate duplicateCandidate) matchPage(contacts []models.Contact, identifiers map[string][]string) (map[string][]string, error) {
	reasons := make(map[string][]string)
	if len(contacts) == 0 {
		return reasons, nil
	}
	ids := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		ids = append(ids, contact.ID)
	}

	if err := matchDuplicateIdentifiers(ids, identifiers, reasons); err != nil {
		return nil, err
	}
	if err := candidate.matchChannels(ids, reasons); err != nil {
		return nil, err
	}
	if err := candidate.matchNames(contacts, reasons); err != nil {
		return nil, err
	}
	return reasons, nil
}

func matchDuplicateIdentifiers(ids []string, identifiers map[string][]string, reasons map[string][]string) error {
	if len(identifiers) > 0 {
		query := orm.From[models.Identifier]().Where(models.IdentifierFields.ContactID.In(ids...)).
			Select(models.IdentifierFields.ContactID, models.IdentifierFields.TypeID, models.IdentifierFields.Value).
			Limit(duplicatePageSize)
		for {
			rows, cursor, err := query.All()
			if err != nil {
				return err
			}
			for _, row := range rows {
				if slices.Contains(identifiers[row.TypeID], row.Value) {
					reasons[row.ContactID] = []string{"identifier"}
				}
			}
			if cursor == "" {
				break
			}
			query.Cursor(cursor)
		}
	}

	return nil
}

func (candidate duplicateCandidate) matchChannels(ids []string, reasons map[string][]string) error {
	phones := phoneSearchValues(strings.TrimSpace(candidate.Phone))
	email := strings.TrimSpace(candidate.Email)
	if len(phones) > 0 || email != "" {
		query := orm.From[models.Channel]().Where(models.ChannelFields.ContactID.In(ids...)).
			Select(models.ChannelFields.ContactID, models.ChannelFields.Kind, models.ChannelFields.Value).
			Limit(duplicatePageSize)
		emails, phoneMatches := make(map[string]bool), make(map[string]bool)
		for {
			rows, cursor, err := query.All()
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.Kind == "email" && email != "" && strings.EqualFold(row.Value, email) {
					emails[row.ContactID] = true
				}
				if row.Kind == "phone" && slices.Contains(phones, phoneDigits(row.Value)) {
					phoneMatches[row.ContactID] = true
				}
			}
			if cursor == "" {
				break
			}
			query.Cursor(cursor)
		}
		for _, id := range ids {
			if emails[id] {
				reasons[id] = append(reasons[id], "email")
			}
			if phoneMatches[id] {
				reasons[id] = append(reasons[id], "phone")
			}
		}
	}

	return nil
}

func (candidate duplicateCandidate) matchNames(contacts []models.Contact, reasons map[string][]string) error {
	name := joinName(strings.TrimSpace(candidate.GivenName), strings.TrimSpace(candidate.OtherNames), strings.TrimSpace(candidate.FamilyName))
	if candidate.Type == "organization" {
		name = strings.TrimSpace(candidate.LegalName)
	}
	if name == "" {
		return nil
	}
	names := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		if contact.Type == candidate.Type {
			names = append(names, contact.FullName)
		}
	}
	if len(names) == 0 {
		return nil
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return err
	}
	// PostgreSQL supplies pg_trgm semantics; every compared name comes from an ORM read.
	matches, err := db.Query[struct{ Name string }](
		`SELECT value AS name FROM jsonb_array_elements_text($1::jsonb) WHERE similarity(value, $2) >= 0.6`,
		[]any{string(encoded), name})
	if err != nil {
		return err
	}
	matched := make(map[string]bool, len(matches))
	for _, match := range matches {
		matched[match.Name] = true
	}
	for _, contact := range contacts {
		if contact.Type == candidate.Type && matched[contact.FullName] {
			reasons[contact.ID] = append(reasons[contact.ID], "name")
		}
	}
	return nil
}

func duplicateFailure() *engine.Response {
	return engine.Error(500, "contacts.contact.duplicates_failed", "Could not check for duplicate contacts", nil)
}
