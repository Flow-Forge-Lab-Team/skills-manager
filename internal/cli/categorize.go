package cli

import "strings"

var categoryKeywords = map[string][]string{
	"Documents":   {"pdf", "docx", "document", "ocr"},
	"Quality":     {"review", "audit", "lint", "test", "debug"},
	"Engineering": {"refactor", "scaffold", "build", "deploy", "ci"},
	"Web":         {"browser", "scrape", "html", "css"},
	"Data":        {"sql", "csv", "spreadsheet", "xlsx", "etl"},
	"AI":          {"prompt", "llm", "rag", "embedding"},
	"Comms":       {"slack", "email", "gmail", "calendar"},
}

func suggestCategories(name, description string) ([]string, []string, int) {
	combined := strings.ToLower(name + " " + description)
	hitCount := 0
	var categories []string
	tagSet := make(map[string]bool)

	for cat, keywords := range categoryKeywords {
		for _, kw := range keywords {
			if strings.Contains(combined, kw) {
				hitCount++
				tagSet[kw] = true
				if !contains(categories, cat) {
					categories = append(categories, cat)
				}
			}
		}
	}

	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}

	return categories, tags, hitCount
}

func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}
