package usersv1

import (
	"encoding/xml"
	"reflect"
	"testing"
)

// TestStructNamedCustomTags proves the (custom_tag_key)/(custom_tag_value)
// field options landed on the generated struct after zararpc-tags applied
// the overlay to users.pb.go.
func TestStructNamedCustomTags(t *testing.T) {
	typ := reflect.TypeOf(StructNamed{})

	flag, ok := typ.FieldByName("Flag")
	if !ok {
		t.Fatal("Flag field not found")
	}
	if got := flag.Tag.Get("xml"); got != "flag,attr" {
		t.Errorf("xml tag = %q, want flag,attr", got)
	}
	if got := flag.Tag.Get("gorm"); got != "primaryKey" {
		t.Errorf("gorm tag = %q, want primaryKey", got)
	}

	name, ok := typ.FieldByName("Name")
	if !ok {
		t.Fatal("Name field not found")
	}
	if got := name.Tag.Get("mapstructure"); got != "name" {
		t.Errorf("mapstructure tag = %q, want name", got)
	}
}

// TestStructNamedXMLMarshal proves the xml tag is honored by encoding/xml:
// Flag is an attribute, Name is an element.
func TestStructNamedXMLMarshal(t *testing.T) {
	data, err := xml.Marshal(StructNamed{Flag: true, Name: "zara"})
	if err != nil {
		t.Fatal(err)
	}
	want := `<StructNamed flag="true"><Name>zara</Name></StructNamed>`
	if got := string(data); got != want {
		t.Errorf("xml = %s, want %s", got, want)
	}
}