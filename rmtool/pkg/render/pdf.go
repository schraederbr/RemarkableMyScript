package render

import (
	"bytes"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jung-kurt/gofpdf"

	"github.com/akeil/rmtool"
	"github.com/akeil/rmtool/internal/logging"
	"github.com/akeil/rmtool/pkg/lines"
)

const (
	tsFormat        = "2006-01-02 15:04:05"
	defaultPageSize = "A4"
)

// Pdf renders all pages of the given document to a PDF file.
//
// The result is written to the given writer.
func Pdf(d *rmtool.Document, w io.Writer) error {
	c := DefaultContext()
	return renderPdf(c, d, w)
}

// PdfPage renders a single drawing into a single one-page PDF.
func PdfPage(c *Context, d *rmtool.Document, pageID string, w io.Writer) error {
	pdf := setupPdf(defaultPageSize, nil)

	err := doRenderPdfPage(c, pdf, d, pageID, 0)
	if err != nil {
		return err
	}

	return pdf.Output(w)
}

func renderPdf(c *Context, d *rmtool.Document, w io.Writer) error {
	if d.FileType() == rmtool.Epub {
		return fmt.Errorf("render Pdf not supported for file type %q", d.FileType())
	}

	logging.Debug("Render PDF for document %q, type %q", d.ID(), d.FileType())
	pdf := setupPdf(defaultPageSize, d)

	var err error
	if d.FileType() == rmtool.Pdf {
		err = overlayPdf(c, d, pdf)
	} else {
		err = drawingsPdf(c, pdf, d)
	}

	if err != nil {
		return err
	}
	return pdf.Output(w)
}

func drawingsPdf(c *Context, pdf *gofpdf.Fpdf, d *rmtool.Document) error {
	for i, pageID := range d.Pages() {
		err := doRenderPdfPage(c, pdf, d, pageID, i)
		if err != nil {
			return err
		}
	}

	return nil
}

func doRenderPdfPage(c *Context, pdf *gofpdf.Fpdf, doc *rmtool.Document, pageID string, i int) error {
	d, err := doc.Drawing(pageID)
	if err != nil {
		return err
	}

	// TODO: determine orientation, rotate image if neccessary
	// and set the page to Landscape
	pdf.AddPage()

	// TODO: add the background template

	return drawingToPdf(c, pdf, d)
}

// drawingToPdf renders the given Drawing to a bitmap and places it on the
// current page of the given PDF.
//
// This function is used to render a drawing onto an empty page
// AND to overlay an existing page with the drawing.
func drawingToPdf(c *Context, pdf *gofpdf.Fpdf, d *lines.Drawing) error {
	id := uuid.New().String()
	opts := gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}

	// render to in-memory PNG
	var buf bytes.Buffer
	err := renderPNG(c, d, false, &buf)
	if err != nil {
		return err
	}
	// pdf.ImageOptions(...) will read frm the registered reader
	pdf.RegisterImageOptionsReader(id, opts, &buf)

	// The drawing will be scaled to the (usable) page width
	wPage, _ := pdf.GetPageSize()
	left, _, right, _ := pdf.GetMargins()
	w := wPage - left - right

	x := 0.0
	y := 0.0
	h := 0.0
	flow := false
	link := 0
	linkStr := ""
	pdf.ImageOptions(id, x, y, w, h, flow, opts, link, linkStr)

	return nil
}

func setupPdf(pageSize string, d *rmtool.Document) *gofpdf.Fpdf {
	orientation := "P" // [P]ortrait or [L]andscape
	sizeUnit := "pt"
	fontDir := ""
	pdf := gofpdf.New(orientation, sizeUnit, pageSize, fontDir)

	//pdf.SetMargins(0, 8, 0) // left, top, right
	pdf.AliasNbPages("{totalPages}")
	pdf.SetFont("helvetica", "", 8)
	pdf.SetTextColor(127, 127, 127)
	pdf.SetProducer("rmtool", true)

	// If we are rendering a complete notebook, add metadata
	if d != nil {
		pdf.SetTitle(d.Name(), true)
		modified := d.LastModified().UTC()
		pdf.SetModificationDate(modified)
		pdf.SetCreationDate(modified)

		pdf.SetFooterFunc(func() {
			pdf.SetY(-20)
			pdf.SetX(24)
			pdf.Cellf(0, 10, "%d / {totalPages}  |  %v (v%d, %v)",
				pdf.PageNo(),
				d.Name(),
				d.Version(),
				d.LastModified().Local().Format(tsFormat))
		})
	}

	return pdf
}
