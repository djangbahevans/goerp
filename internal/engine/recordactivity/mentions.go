package recordactivity

import "regexp"

// MaxMentions is how many distinct users one comment can mention
// (record-activity.md §9).
const MaxMentions = 20

// mentionToken is a mention in a comment body: "<@", a user id in its
// canonical lowercase UUID form, and ">". Anything else is plain text.
var mentionToken = regexp.MustCompile(`<@([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})>`)

// MentionIDs returns the distinct user ids body's mention tokens name, in
// order of first appearance.
func MentionIDs(body string) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, m := range mentionToken.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

// RenderMentions replaces each mention token in body with "@" and the
// name label returns for its user id.
func RenderMentions(body string, label func(userID string) string) string {
	return mentionToken.ReplaceAllStringFunc(body, func(token string) string {
		return "@" + label(token[2:len(token)-1])
	})
}
