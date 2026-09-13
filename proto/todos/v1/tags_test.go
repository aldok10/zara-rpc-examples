package todosv1

import (
	"reflect"
	"testing"
)

// TestTodoCustomTags proves the (zara.options.tags) field option landed on
// the generated Todo struct: the gorm + mapstructure tags that make the
// struct usable directly as a GORM model (SQLite) and mappable with
// mapstructure, with no hand-written mapping struct. This is the regression
// guard that tag patching survives regeneration.
func TestTodoCustomTags(t *testing.T) {
	typ := reflect.TypeOf(Todo{})

	cases := []struct {
		field        string
		gorm         string
		mapstructure string
	}{
		{"Id", "primaryKey;size:36", "id"},
		{"OwnerId", "index;size:36", "owner_id"},
		{"Title", "size:255;not null", "title"},
		{"Done", "default:false", "done"},
		{"CreatedAt", "autoCreateTime", "created_at"},
		{"UpdatedAt", "autoUpdateTime", "updated_at"},
	}
	for _, tc := range cases {
		f, ok := typ.FieldByName(tc.field)
		if !ok {
			t.Fatalf("field %s not found", tc.field)
		}
		if got := f.Tag.Get("gorm"); got != tc.gorm {
			t.Errorf("%s gorm tag = %q, want %q", tc.field, got, tc.gorm)
		}
		if got := f.Tag.Get("mapstructure"); got != tc.mapstructure {
			t.Errorf("%s mapstructure tag = %q, want %q", tc.field, got, tc.mapstructure)
		}
	}
}