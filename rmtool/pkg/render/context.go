package render

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/akeil/rmtool"
	"github.com/akeil/rmtool/internal/imaging"
	"github.com/akeil/rmtool/internal/logging"
	"github.com/akeil/rmtool/pkg/lines"
)

var brushNames = map[lines.BrushType]string{
	lines.Ballpoint:          "ballpoint",
	lines.BallpointV5:        "ballpoint",
	lines.Pencil:             "pencil",
	lines.PencilV5:           "pencil",
	lines.MechanicalPencil:   "mech-pencil",
	lines.MechanicalPencilV5: "mech-pencil",
	lines.Marker:             "marker",
	lines.MarkerV5:           "marker",
	lines.Fineliner:          "fineliner",
	lines.FinelinerV5:        "fineliner",
	lines.Highlighter:        "highlighter",
	lines.HighlighterV5:      "highlighter",
	lines.PaintBrush:         "ballpoint", // TODO add mask image and change name
	lines.PaintBrushV5:       "ballpoint", // TODO add mask image and change name
	lines.CalligraphyV5:      "ballpoint", // TODO add mask image and change name
}

var defaultColors = map[lines.BrushColor]color.Color{
	lines.Black: color.Black,
	lines.Gray:  color.RGBA{150, 150, 150, 255},
	lines.White: color.White,
}

// Context holds parameters and cached data for rendering operations.
//
// If multiple drawings are rendered, they should use the same Context.
type Context struct {
	DataDir     string
	palette     *Palette
	sprites     *image.RGBA
	spriteIndex map[string][]int
	spriteMx    sync.Mutex
	tplCache    map[string]image.Image
	tplMx       sync.Mutex
}

// NewContext sets up a new rendering context.
//
// dataDir should point to a directory with a spritesheet for the brushes
// and a subdirectory 'templates' with page backgrounds.
func NewContext(dataDir string, p *Palette) *Context {
	return &Context{
		DataDir: dataDir,
		palette: p,
	}
}

// DefaultContext creates a new rendering context with default settings.
func DefaultContext() *Context {
	gray := color.RGBA{150, 150, 150, 255}
	// TODO hardcoded path - choose a more sensible value
	return NewContext("./data", NewPalette(color.White, gray, defaultColors))
}

// Page draws a single page to a PNG and writes it to the given writer.
func (c *Context) Page(doc *rmtool.Document, pageID string, w io.Writer) error {
	return renderPage(c, doc, pageID, w)
}

// Pdf renders all pages from a document to a PDF file.
//
// The resulting PDF document is written to the given writer.
func (c *Context) Pdf(doc *rmtool.Document, w io.Writer) error {
	return renderPdf(c, doc, w)
}

func (c *Context) loadBrush(bt lines.BrushType, bc lines.BrushColor) (Brush, error) {
	col := c.palette.Color(bc)
	if col == nil {
		return nil, fmt.Errorf("invalid color %v", bc)
	}

	name := brushNames[bt]
	if name == "" {
		return nil, fmt.Errorf("unsupported brush type %v", bt)
	}

	img, err := c.loadBrushMask(name)
	if err != nil {
		return nil, err
	}
	mask := imaging.CreateMask(img)

	switch bt {
	case lines.Ballpoint, lines.BallpointV5:
		return &Ballpoint{
			mask:  mask,
			fill:  image.NewUniform(col),
			color: col,
		}, nil
	case lines.Pencil, lines.PencilV5:
		return &Pencil{
			mask: mask,
			fill: image.NewUniform(col),
		}, nil
	case lines.MechanicalPencil, lines.MechanicalPencilV5:
		return &MechanicalPencil{
			mask: mask,
			fill: image.NewUniform(col),
		}, nil
	case lines.Marker, lines.MarkerV5:
		return &Marker{
			mask: mask,
			fill: image.NewUniform(col),
		}, nil
	case lines.Fineliner, lines.FinelinerV5:
		return &Fineliner{
			mask:  mask,
			fill:  image.NewUniform(col),
			color: col,
		}, nil
	case lines.Highlighter, lines.HighlighterV5:
		return &Highlighter{
			mask: mask,
			fill: image.NewUniform(c.palette.Highlighter),
		}, nil
	case lines.PaintBrush, lines.PaintBrushV5:
		return &Paintbrush{
			fill: image.NewUniform(col),
		}, nil
	default:
		logging.Warning("unsupported brush type %v", bt)
		return loadBasePen(mask, col), nil
	}
}

// loadBrushMask loads a brush image identified by name.
func (c *Context) loadBrushMask(name string) (image.Image, error) {
	err := c.lazyLoadSpritesheet()
	if err != nil {
		return nil, err
	}

	idx := c.spriteIndex[name]
	if idx == nil {
		return nil, fmt.Errorf("no sprite image for brush %q", name)
	} else if len(idx) != 4 {
		return nil, fmt.Errorf("invalid sprite entry for brush %q", name)
	}

	rect := image.Rect(idx[0], idx[1], idx[2], idx[3])

	// sanity check
	if rect.Dx() > c.sprites.Bounds().Dx() || rect.Dy() > c.sprites.Bounds().Dy() {
		return nil, fmt.Errorf("sprite bounds not within spritesheet dimensions")
	}

	return c.sprites.SubImage(rect), nil
}

func (c *Context) lazyLoadSpritesheet() error {
	c.spriteMx.Lock()
	defer c.spriteMx.Unlock()
	if c.sprites != nil {
		// already loaded
		return nil
	}

	// index map
	jsonPath := filepath.Join(c.DataDir, "sprites.json")
	logging.Debug("Load sprite index from %q", jsonPath)
	jsonFile, err := os.Open(jsonPath)
	if err != nil {
		return err
	}
	defer jsonFile.Close()
	err = json.NewDecoder(jsonFile).Decode(&c.spriteIndex)
	if err != nil {
		return err
	}

	// image
	img, err := readPNG(c.DataDir, "sprites.png")
	if err != nil {
		return err
	}

	// type Image to type RGBA (allows SubImage(...)
	c.sprites = image.NewRGBA(img.Bounds())
	for x := 0; x < c.sprites.Bounds().Dx(); x++ {
		for y := 0; y < c.sprites.Bounds().Dy(); y++ {
			c.sprites.Set(x, y, img.At(x, y))
		}
	}

	return nil
}

func (c *Context) loadTemplate(name string) (image.Image, error) {
	c.tplMx.Lock()
	defer c.tplMx.Unlock()
	if c.tplCache == nil {
		c.tplCache = make(map[string]image.Image)
	}
	cached := c.tplCache[name]
	if cached != nil {
		return cached, nil
	}

	img, err := readPNG(c.DataDir, "templates", name+".png")
	if err != nil {
		return nil, err
	}

	c.tplCache[name] = img

	return img, nil
}

func readPNG(path ...string) (image.Image, error) {
	p := filepath.Join(path...)
	logging.Debug("Read PNG image from %q", p)

	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return png.Decode(f)
}

// Palette holds the colors used for rendering.
//
// You can use a palette to map from the default colors of the reMarkable
// tablet (black, gray, white) ro another color scheme.
type Palette struct {
	Background  color.Color
	Highlighter color.Color
	colors      map[lines.BrushColor]color.Color
}

// NewPalette creates a new palette with the given color scheme.
func NewPalette(bg color.Color, highlighter color.Color, brushColors map[lines.BrushColor]color.Color) *Palette {
	return &Palette{
		Background:  bg,
		colors:      brushColors,
		Highlighter: highlighter,
	}
}

// Color is used by the renderer to retrieve the color value to use
// for a specific Brush Color.
func (p *Palette) Color(bc lines.BrushColor) color.Color {
	c, ok := p.colors[bc]
	if ok {
		return c
	}
	return defaultColors[bc]
}
