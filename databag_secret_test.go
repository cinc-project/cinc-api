package cinc

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDataBagSecret(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare", "s3cret", "s3cret"},
		{"trailing newline", "s3cret\n", "s3cret"},
		{"crlf", "s3cret\r\n", "s3cret"},
		{"surrounding ascii whitespace", " \t\v\f\r\ns3cret \t\v\f\r\n", "s3cret"},
		// Ruby's String#strip also removes NUL bytes at either end.
		{"nul padding", "\x00s3cret\x00\x00", "s3cret"},
		{"inner whitespace kept", "s3 cr\net", "s3 cr\net"},
		// Ruby strips ASCII whitespace only; a non-breaking space is data.
		{"unicode space kept", " s3cret ", " s3cret "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDataBagSecret([]byte(tt.in))
			if err != nil {
				t.Fatalf("ParseDataBagSecret: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("ParseDataBagSecret(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseDataBagSecret_DoesNotAliasInput(t *testing.T) {
	in := []byte(" s3cret\n")
	got, err := ParseDataBagSecret(in)
	if err != nil {
		t.Fatal(err)
	}
	got[0] = 'X'
	if string(in) != " s3cret\n" {
		t.Errorf("input modified through the result: %q", in)
	}
}

func TestParseDataBagSecret_Empty(t *testing.T) {
	for _, in := range []string{"", "\n", " \t\r\n\x00"} {
		if _, err := ParseDataBagSecret([]byte(in)); !errors.Is(err, ErrEmptyDataBagSecret) {
			t.Errorf("ParseDataBagSecret(%q) err = %v, want ErrEmptyDataBagSecret", in, err)
		}
	}
}

func TestLoadDataBagSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDataBagSecret(path)
	if err != nil {
		t.Fatalf("LoadDataBagSecret: %v", err)
	}
	if string(got) != "s3cret" {
		t.Errorf("LoadDataBagSecret = %q, want s3cret", got)
	}
}

func TestLoadDataBagSecret_Missing(t *testing.T) {
	_, err := LoadDataBagSecret(filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist chain", err)
	}
}

func TestLoadDataBagSecret_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDataBagSecret(path)
	if !errors.Is(err, ErrEmptyDataBagSecret) {
		t.Fatalf("err = %v, want ErrEmptyDataBagSecret", err)
	}
	if !contains(err.Error(), path) {
		t.Errorf("error %q should name the file", err)
	}
}

// Chef reads the secret file as UTF-8 text, and Ruby's String#strip raises on
// an invalid byte sequence, so knife and chef-client refuse such a secret.
// Accepting it would encrypt items nothing else can decrypt with that file.
func TestParseDataBagSecret_InvalidUTF8(t *testing.T) {
	_, err := ParseDataBagSecret([]byte("\xff\xfeabc"))
	if err == nil {
		t.Fatal("expected an error for a secret that is not valid UTF-8")
	}
	if errors.Is(err, ErrEmptyDataBagSecret) {
		t.Errorf("err = %v, should not be ErrEmptyDataBagSecret", err)
	}
	if !errors.Is(err, ErrInvalidDataBagSecret) {
		t.Errorf("err = %v, want ErrInvalidDataBagSecret", err)
	}
}

func TestLoadDataBagSecret_InvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("\xff\xfeabc"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDataBagSecret(path)
	if !errors.Is(err, ErrInvalidDataBagSecret) {
		t.Fatalf("err = %v, want ErrInvalidDataBagSecret", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want it to name %s", err, path)
	}
}
