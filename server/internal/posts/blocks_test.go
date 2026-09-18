package posts

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateBlocksAcceptsAWholeArticle(t *testing.T) {
	mediaID, recipeID := uuid.NewString(), uuid.NewString()
	problems, out := ValidateBlocks([]Block{
		{Type: BlockHeading, Text: "  Как варить борщ  ", Level: 9},
		{Type: BlockParagraph, Text: "Первый абзац."},
		{Type: BlockList, Items: []string{"свёкла", "  ", "капуста"}},
		{Type: BlockPhoto, MediaID: mediaID, Caption: "Подпись"},
		{Type: BlockRecipe, RecipeID: recipeID},
	})
	if problems != nil {
		t.Fatalf("valid article rejected: %v", problems)
	}
	if out[0].Text != "Как варить борщ" {
		t.Errorf("heading not trimmed: %q", out[0].Text)
	}
	// Level 1 belongs to the post title, so anything out of range folds to 2.
	if out[0].Level != 2 {
		t.Errorf("heading level = %d, want 2", out[0].Level)
	}
	if len(out[2].Items) != 2 {
		t.Errorf("blank list item survived: %v", out[2].Items)
	}
}

func TestValidateBlocksRejectsBadShapes(t *testing.T) {
	long := strings.Repeat("я", MaxTextLength+1)
	for name, block := range map[string]Block{
		"unknown type":     {Type: "iframe", Text: "x"},
		"no type":          {Text: "x"},
		"empty paragraph":  {Type: BlockParagraph, Text: "   "},
		"overlong text":    {Type: BlockParagraph, Text: long},
		"empty heading":    {Type: BlockHeading, Text: ""},
		"empty list":       {Type: BlockList, Items: nil},
		"blank-only list":  {Type: BlockList, Items: []string{" ", ""}},
		"photo without id": {Type: BlockPhoto},
		"photo bad id":     {Type: BlockPhoto, MediaID: "not-a-uuid"},
		"empty gallery":    {Type: BlockGallery},
		"recipe bad id":    {Type: BlockRecipe, RecipeID: "nope"},
	} {
		t.Run(name, func(t *testing.T) {
			problems, _ := ValidateBlocks([]Block{block})
			if len(problems) == 0 {
				t.Errorf("accepted an invalid block: %+v", block)
			}
			for _, msg := range problems {
				if msg == "" {
					t.Error("problem reported with no message")
				}
			}
		})
	}
}

func TestGalleryDropsDuplicates(t *testing.T) {
	id := uuid.NewString()
	problems, out := ValidateBlocks([]Block{{Type: BlockGallery, MediaIDs: []string{id, id}}})
	if problems != nil {
		t.Fatalf("rejected: %v", problems)
	}
	if len(out[0].MediaIDs) != 1 {
		t.Errorf("duplicate media survived: %v", out[0].MediaIDs)
	}
}

func TestTooManyBlocksRejected(t *testing.T) {
	blocks := make([]Block, MaxBlocks+1)
	for i := range blocks {
		blocks[i] = Block{Type: BlockParagraph, Text: "x"}
	}
	if problems, _ := ValidateBlocks(blocks); len(problems) == 0 {
		t.Error("a body over the block limit was accepted")
	}
}

func TestMediaRefsCoversEveryCarrier(t *testing.T) {
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	blocks := []Block{
		{Type: BlockPhoto, MediaID: a},
		{Type: BlockVideo, MediaID: b},
		{Type: BlockGallery, MediaIDs: []string{c}},
		{Type: BlockParagraph, Text: "no media here"},
	}
	media, _ := refsOf(blocks)
	if len(media) != 3 {
		t.Errorf("refsOf found %d media ids, want 3 — a missed carrier means an "+
			"unvalidated reference reaches the database", len(media))
	}
}
