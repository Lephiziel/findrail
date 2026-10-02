package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/extract/text"
)

// Fixture is generated from PDF objects, never copied from a user's document.
func fixture(bodies ...string) []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	kids := []string{}
	for _, body := range bodies {
		id := len(objects) + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", id))
		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", body)
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", id+1), fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(bodies), strings.Join(kids, " "))
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, n := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", n)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}
func TestPageNumbersAndLimits(t *testing.T) {
	pages, err := Parse(context.Background(), fixture("intro evidence", "", "webhook retry"))
	if err != nil || len(pages) != 3 || pages[2].Number != 3 || !strings.Contains(pages[2].Text, "webhook retry") {
		t.Fatalf("pages: %+v %v", pages, err)
	}
	if _, err := Parse(context.Background(), fixture("")); !errors.Is(err, text.ErrUnsupported) {
		t.Fatalf("textless PDF: %v", err)
	}
	if _, err := Parse(context.Background(), []byte("%PDF-broken")); err == nil {
		t.Fatal("accepted invalid PDF")
	}
	if _, err := Parse(context.Background(), fixture(strings.Repeat("x", MaxText+1))); !errors.Is(err, text.ErrTooLarge) {
		t.Fatalf("text budget: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, fixture("evidence")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	var output bytes.Buffer
	if err := Worker(context.Background(), bytes.NewReader(fixture("")), &output); err != nil || strings.TrimSpace(output.String()) != "[]" {
		t.Fatalf("worker no text: %s %v", output.String(), err)
	}
}
