package cli

import (
	"slices"
	"testing"
)

func TestSuggestCategoriesDeterministic(t *testing.T) {
	// Use a skill name and description that match multiple categories and tags
	name := "review-document"
	description := "A skill that audits and lint checks HTML documents"

	// Call the function 10 times and verify all results are identical
	results := make([]struct {
		categories []string
		tags       []string
	}, 10)

	for i := 0; i < 10; i++ {
		cats, tags, _ := suggestCategories(name, description)
		results[i].categories = cats
		results[i].tags = tags
	}

	// Verify all categories are identical across runs
	for i := 1; i < 10; i++ {
		if !slices.Equal(results[0].categories, results[i].categories) {
			t.Errorf("categories not deterministic at run %d: got %v, want %v",
				i, results[i].categories, results[0].categories)
		}
	}

	// Verify all tags are identical across runs
	for i := 1; i < 10; i++ {
		if !slices.Equal(results[0].tags, results[i].tags) {
			t.Errorf("tags not deterministic at run %d: got %v, want %v",
				i, results[i].tags, results[0].tags)
		}
	}

	// Verify the results are sorted
	if !slices.IsSorted(results[0].categories) {
		t.Errorf("categories not sorted: %v", results[0].categories)
	}
	if !slices.IsSorted(results[0].tags) {
		t.Errorf("tags not sorted: %v", results[0].tags)
	}

	// Verify we got expected categories and tags
	if len(results[0].categories) < 2 {
		t.Errorf("expected at least 2 categories, got %d", len(results[0].categories))
	}
	if len(results[0].tags) < 2 {
		t.Errorf("expected at least 2 tags, got %d", len(results[0].tags))
	}
}
