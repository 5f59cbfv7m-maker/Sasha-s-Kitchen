package posts

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Block kinds. These are the contract with every client, so the set is closed:
// an unknown type is rejected on write rather than stored and skipped on read,
// because a block nobody can render is a hole in someone's published article.
const (
	BlockParagraph = "paragraph"
	BlockHeading   = "heading"
	BlockQuote     = "quote"
	BlockList      = "list"
	BlockPhoto     = "photo"
	BlockGallery   = "gallery"
	BlockVideo     = "video"
	BlockRecipe    = "recipe"
)

// Limits. Generous enough for a long recipe essay, bounded enough that one
// post cannot become a denial-of-service payload.
const (
	MaxBlocks         = 200
	MaxTextLength     = 5000
	MaxCaptionLength  = 300
	MaxListItems      = 50
	MaxListItemLength = 500
	MaxGalleryItems   = 20
)

// Block is one element of a post body as stored in posts.body_blocks.
//
// A flat struct with optional fields rather than an interface per type: the
// column is jsonb either way, and a closed set of eight kinds does not earn
// the indirection.
type Block struct {
	Type string `json:"type"`

	// paragraph, heading, quote
	Text string `json:"text,omitempty"`
	// heading only: 2 or 3. Level 1 is the post title.
	Level int `json:"level,omitempty"`

	// list
	Ordered bool     `json:"ordered,omitempty"`
	Items   []string `json:"items,omitempty"`

	// photo, video
	MediaID string `json:"media_id,omitempty"`
	// gallery
	MediaIDs []string `json:"media_ids,omitempty"`
	// photo, gallery, video
	Caption string `json:"caption,omitempty"`

	// recipe: the embedded card a reader takes straight into their kitchen.
	RecipeID string `json:"recipe_id,omitempty"`
}

// MediaRefs lists every media id this block points at.
func (b Block) MediaRefs() []string {
	switch b.Type {
	case BlockPhoto, BlockVideo:
		if b.MediaID != "" {
			return []string{b.MediaID}
		}
	case BlockGallery:
		return b.MediaIDs
	}
	return nil
}

// DecodeBlocks parses a stored body.
func DecodeBlocks(raw []byte) ([]Block, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []Block
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("posts: decode blocks: %w", err)
	}
	return out, nil
}

// ValidateBlocks checks the shape of a body and normalises it in place,
// returning per-field Russian messages keyed by block position.
//
// It deliberately does NOT check that the referenced media and recipes exist
// or belong to the author: that needs the database, and doing it here would
// make the pure shape rules untestable without one. Repo.ValidateRefs is the
// second half.
func ValidateBlocks(blocks []Block) (map[string]string, []Block) {
	problems := map[string]string{}
	if len(blocks) > MaxBlocks {
		problems["blocks"] = fmt.Sprintf("Не более %d блоков", MaxBlocks)
		return problems, nil
	}

	out := make([]Block, 0, len(blocks))
	for i, b := range blocks {
		field := fmt.Sprintf("blocks[%d]", i)
		b.Type = strings.TrimSpace(b.Type)

		switch b.Type {
		case BlockParagraph, BlockQuote:
			b.Text = strings.TrimSpace(b.Text)
			if b.Text == "" {
				problems[field] = "Пустой текстовый блок"
			} else if len([]rune(b.Text)) > MaxTextLength {
				problems[field] = fmt.Sprintf("Не более %d символов", MaxTextLength)
			}

		case BlockHeading:
			b.Text = strings.TrimSpace(b.Text)
			if b.Text == "" {
				problems[field] = "Пустой заголовок"
			} else if len([]rune(b.Text)) > MaxCaptionLength {
				problems[field] = fmt.Sprintf("Не более %d символов", MaxCaptionLength)
			}
			// Level 1 belongs to the post title, so a body heading starts at 2.
			if b.Level != 2 && b.Level != 3 {
				b.Level = 2
			}

		case BlockList:
			if len(b.Items) == 0 {
				problems[field] = "Пустой список"
			} else if len(b.Items) > MaxListItems {
				problems[field] = fmt.Sprintf("Не более %d пунктов", MaxListItems)
			} else {
				items := make([]string, 0, len(b.Items))
				for _, it := range b.Items {
					it = strings.TrimSpace(it)
					if it == "" {
						continue
					}
					if len([]rune(it)) > MaxListItemLength {
						problems[field] = fmt.Sprintf("Пункт длиннее %d символов", MaxListItemLength)
						break
					}
					items = append(items, it)
				}
				if len(items) == 0 && problems[field] == "" {
					problems[field] = "Пустой список"
				}
				b.Items = items
			}

		case BlockPhoto, BlockVideo:
			if _, err := uuid.Parse(b.MediaID); err != nil {
				problems[field] = "Некорректный идентификатор файла"
			}
			b.Caption = trimCaption(b.Caption)

		case BlockGallery:
			switch {
			case len(b.MediaIDs) == 0:
				problems[field] = "Пустая галерея"
			case len(b.MediaIDs) > MaxGalleryItems:
				problems[field] = fmt.Sprintf("Не более %d файлов в галерее", MaxGalleryItems)
			default:
				seen := map[string]bool{}
				ids := make([]string, 0, len(b.MediaIDs))
				for _, id := range b.MediaIDs {
					if _, err := uuid.Parse(id); err != nil {
						problems[field] = "Некорректный идентификатор файла"
						break
					}
					// The same photo twice in one gallery is a client bug, not
					// an intent; dropping it is kinder than rendering it twice.
					if seen[id] {
						continue
					}
					seen[id] = true
					ids = append(ids, id)
				}
				b.MediaIDs = ids
			}
			b.Caption = trimCaption(b.Caption)

		case BlockRecipe:
			if _, err := uuid.Parse(b.RecipeID); err != nil {
				problems[field] = "Некорректный идентификатор рецепта"
			}

		case "":
			problems[field] = "Не указан тип блока"
		default:
			problems[field] = fmt.Sprintf("Неизвестный тип блока: %s", b.Type)
		}

		out = append(out, b)
	}
	if len(problems) > 0 {
		return problems, nil
	}
	return nil, out
}

func trimCaption(s string) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > MaxCaptionLength {
		return string([]rune(s)[:MaxCaptionLength])
	}
	return s
}
