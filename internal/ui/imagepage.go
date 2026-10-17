package ui

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
)

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true}

// ImagePage previews an image using half-block cells (two pixels per cell).
type ImagePage struct {
	app   *App
	path  string
	img   image.Image
	err   error
	zoom  float64 // 0 = fit
	size  int64
}

func (a *App) openImage(path string) {
	p := &ImagePage{app: a, path: path}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		p.img, _, p.err = image.Decode(f)
		if st, err := f.Stat(); err == nil {
			p.size = st.Size()
		}
	} else {
		p.err = err
	}
	a.openSpecial(p)
	a.addRecentFile(path)
}

// Title implements SpecialPage.
func (p *ImagePage) Title() string { return filepath.Base(p.path) }

// HandleKey implements SpecialPage.
func (p *ImagePage) HandleKey(key, text string, isText bool) bool {
	switch key {
	case "+", "=", "ctrl+=":
		p.zoomBy(1.25)
	case "-", "ctrl+-":
		p.zoomBy(0.8)
	case "0":
		p.zoom = 0
	default:
		return false
	}
	return true
}

func (p *ImagePage) zoomBy(f float64) {
	if p.zoom == 0 {
		p.zoom = p.fitScale(p.app.rEditor)
	}
	p.zoom = clampFloat(p.zoom*f, 0.01, 32)
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// fitScale returns the scale that fits the image in r (pixels per source pixel).
func (p *ImagePage) fitScale(r Rect) float64 {
	b := p.img.Bounds()
	sw := float64(r.W) / float64(b.Dx())
	sh := float64((r.H-1)*2) / float64(b.Dy())
	s := sw
	if sh < s {
		s = sh
	}
	if s > 1 && p.zoom == 0 {
		// never upscale small images when fitting
		s = 1
	}
	return s
}

// Draw implements SpecialPage.
func (p *ImagePage) Draw(r Rect) {
	a := p.app
	th := a.theme
	bgc := th.C("editor.background")
	if p.err != nil || p.img == nil {
		msg := "The image could not be displayed."
		if p.err != nil {
			msg += " " + p.err.Error()
		}
		a.cv.TextCenter(r.X, r.Y+r.H/2, r.W, msg, styleFB(th.C("descriptionForeground"), bgc))
		return
	}
	b := p.img.Bounds()
	scale := p.zoom
	if scale == 0 {
		scale = p.fitScale(r)
	}
	w := int(float64(b.Dx()) * scale)
	h := int(float64(b.Dy()) * scale)
	cols := max(min(w, r.W), 1)
	rows := max(min((h+1)/2, r.H-1), 1)
	x0 := r.X + (r.W-cols)/2
	y0 := r.Y + (r.H-1-rows)/2
	// box-filter the source pixels covered by each target pixel for smooth downscaling
	sample := func(px, py int) (tcell.Color, bool) {
		x0 := float64(px) / scale
		y0 := float64(py) / scale
		x1 := float64(px+1) / scale
		y1 := float64(py+1) / scale
		sx0, sy0 := b.Min.X+int(x0), b.Min.Y+int(y0)
		sx1, sy1 := b.Min.X+max(int(x1), int(x0)+1), b.Min.Y+max(int(y1), int(y0)+1)
		if sx0 >= b.Max.X || sy0 >= b.Max.Y {
			return bgc, false
		}
		sx1, sy1 = min(sx1, b.Max.X), min(sy1, b.Max.Y)
		// cap the work per cell on huge images
		stepX := max((sx1-sx0)/6, 1)
		stepY := max((sy1-sy0)/6, 1)
		var rs, gs, bs, as, n float64
		for sy := sy0; sy < sy1; sy += stepY {
			for sx := sx0; sx < sx1; sx += stepX {
				cr, cg, cb, ca := p.img.At(sx, sy).RGBA()
				rs += float64(cr)
				gs += float64(cg)
				bs += float64(cb)
				as += float64(ca)
				n++
			}
		}
		alpha := as / n / 0xffff
		cr, cg, cb := rs/n/257, gs/n/257, bs/n/257
		if alpha < 1 {
			// premultiplied colors over a checkerboard like VS Code's image preview
			br, bgg, bb := checker(px, py).RGB()
			cr += float64(br) * (1 - alpha)
			cg += float64(bgg) * (1 - alpha)
			cb += float64(bb) * (1 - alpha)
		}
		return tcell.NewRGBColor(int32(cr), int32(cg), int32(cb)), true
	}
	for cy := 0; cy < rows; cy++ {
		for cx := 0; cx < cols; cx++ {
			top, _ := sample(cx, cy*2)
			bot, ok := sample(cx, cy*2+1)
			if !ok {
				bot = bgc
			}
			a.cv.Set(x0+cx, y0+cy, '▀', styleFB(top, bot))
		}
	}
	info := fmt.Sprintf("%dx%d  %s  %d%%", b.Dx(), b.Dy(), humanSize(p.size), int(scale*100))
	a.cv.TextCenter(r.X, r.Y+r.H-1, r.W, info+"   (+/- to zoom, 0 to fit)", styleFB(th.C("descriptionForeground"), bgc))
	a.addHit(r, func(ev MouseEvent) bool {
		if ev.Kind == MouseWheel && ev.Mods&tcell.ModCtrl != 0 {
			if ev.WheelDY < 0 {
				p.zoomBy(1.25)
			} else {
				p.zoomBy(0.8)
			}
			return true
		}
		return false
	})
}

func checker(x, y int) tcell.Color {
	if (x/4+y/4)%2 == 0 {
		return tcell.NewRGBColor(0x40, 0x40, 0x40)
	}
	return tcell.NewRGBColor(0x30, 0x30, 0x30)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func isImagePath(p string) bool { return imageExts[strings.ToLower(filepath.Ext(p))] }
