package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"

	"golang.org/x/image/draw"
)

const (
	fullScaling = 2
	targetGamma = 1 / .023
	sourceGamma = 2.2 // this is the common default.  Use this since Go doesn't expose it.

	nrgba64Max = 0xFFFF
	nrgbaMax   = 0xFF
)

var thumbnailDarkenFactor = math.Pow( /*darkest pixel=*/ math.Nextafter(0.5, 0)/nrgbaMax, 1/targetGamma)

type errchain struct {
	msg   string
	cause error
}

func (e *errchain) Error() string {
	msg := e.msg
	if e.cause != nil {
		msg += "\n\tCaused by\n" + e.cause.Error()
	}
	return msg
}

func chain(cause error, params ...interface{}) *errchain {
	msg := fmt.Sprintln(params...)
	return &errchain{
		msg:   msg[0 : len(msg)-2],
		cause: cause,
	}
}

var (
	stretch = flag.Bool("stretch", true, "If true, stretches the Full(back) image to fit the"+
		" Thumbnail(front) image.  If false, the Full image will be scaled proportionally to fit"+
		" and centered.")

	dither = flag.Bool("dither", true, "If true, dithers the Full(back) image to hide banding."+
		"  Use if the Full image doesn't contain text nor is already using few colors"+
		" (such as comics).")

	thumbnail   = flag.String("thumbnail", "", "The file path of the Thumbnail(front) image")
	full        = flag.String("full", "", "The file path of the Full(back) image")
	dest        = flag.String("dest", "", "The dest file path of the PNG image")
	webfallback = flag.Bool(
		"webfallback", true, "If true, enable a web UI fallback at http://localhost:8080/")
)

// Linearize image.  At leats 16 bits per channel are needed as per
// http://lbodnar.dsl.pipex.com/imaging/gamma.html
func linearImage(srcim image.Image, gamma float64) *image.NRGBA64 {
	dstim := image.NewNRGBA64(image.Rectangle{
		Max: image.Point{
			X: srcim.Bounds().Dx(),
			Y: srcim.Bounds().Dy(),
		},
	})
	var dsty int
	for srcy := srcim.Bounds().Min.Y; srcy < srcim.Bounds().Max.Y; srcy++ {
		var dstx int
		for srcx := srcim.Bounds().Min.X; srcx < srcim.Bounds().Max.X; srcx++ {
			nrgba64 := color.NRGBA64Model.Convert(srcim.At(srcx, srcy)).(color.NRGBA64)
			nrgba64.R = uint16(nrgba64Max * math.Pow(float64(nrgba64.R)/nrgba64Max, gamma))
			nrgba64.G = uint16(nrgba64Max * math.Pow(float64(nrgba64.G)/nrgba64Max, gamma))
			nrgba64.B = uint16(nrgba64Max * math.Pow(float64(nrgba64.B)/nrgba64Max, gamma))
			// Alpha is not affected
			dstim.SetNRGBA64(dstx, dsty, nrgba64)
			dstx++
		}
		dsty++
	}
	return dstim
}

func darkenImage(srcim image.Image, scale float64) *image.NRGBA64 {
	dstim := image.NewNRGBA64(image.Rectangle{
		Max: image.Point{
			X: srcim.Bounds().Dx(),
			Y: srcim.Bounds().Dy(),
		},
	})
	var dsty int
	for srcy := srcim.Bounds().Min.Y; srcy < srcim.Bounds().Max.Y; srcy++ {
		var dstx int
		for srcx := srcim.Bounds().Min.X; srcx < srcim.Bounds().Max.X; srcx++ {
			nrgba64 := color.NRGBA64Model.Convert(srcim.At(srcx, srcy)).(color.NRGBA64)
			nrgba64.R = uint16(float64(nrgba64.R) * scale)
			nrgba64.G = uint16(float64(nrgba64.G) * scale)
			nrgba64.B = uint16(float64(nrgba64.B) * scale)
			// Alpha is not affected
			dstim.SetNRGBA64(dstx, dsty, nrgba64)
			dstx++
		}
		dsty++
	}
	return dstim
}

// Assumes src  is linear
func resize(src image.Image, targetBounds image.Rectangle, targetScaleDown int, stretch bool) (
	*image.NRGBA64, int, int) {
	var xoffset, yoffset int
	var newTargetBounds image.Rectangle
	if stretch {
		newTargetBounds = image.Rectangle{
			Max: image.Point{
				X: targetBounds.Dx() / targetScaleDown,
				Y: targetBounds.Dy() / targetScaleDown,
			},
		}
	} else {
		// Check if the source image is wider than the dest, or narrower.   The odd multiplication
		// avoids casting to float, at the risk of possibly overflow.  Don't use images taller or
		// wider than 32K on 32 bits machines.
		if src.Bounds().Dx()*targetBounds.Dy() > targetBounds.Dx()*src.Bounds().Dy() {
			// source image is wider.
			newTargetBounds = image.Rectangle{
				Max: image.Point{
					X: targetBounds.Dx() / targetScaleDown,
					Y: src.Bounds().Dy() * targetBounds.Dx() / src.Bounds().Dx() / targetScaleDown,
				},
			}
			yoffset = (targetBounds.Dy() - newTargetBounds.Dy()*targetScaleDown) / 2
		} else {
			// source image is narrower.
			newTargetBounds = image.Rectangle{
				Max: image.Point{
					X: src.Bounds().Dx() * targetBounds.Dy() / src.Bounds().Dy() / targetScaleDown,
					Y: targetBounds.Dy() / targetScaleDown,
				},
			}
			xoffset = (targetBounds.Dx() - newTargetBounds.Dx()*targetScaleDown) / 2
		}
	}

	dst := image.NewNRGBA64(newTargetBounds)
	scaler := draw.CatmullRom
	scaler.Scale(dst, newTargetBounds, src, src.Bounds(), draw.Over, nil)
	return dst, xoffset, yoffset
}

type dithererr struct {
	r, g, b float64
}

func scaleClamp(v float64, max float64) float64 {
	if v > 1.0 {
		v = 1.0
	}
	return math.Round(v * max)
}

func calculateFullPixel(
	srcx int, srcnrgba color.NRGBA64, dither bool, errcurr, errnext []dithererr) color.NRGBA {
	const newMaxValue = nrgbaMax
	nonneg := func(in float64) float64 {
		if low := 1.0 / newMaxValue; in < low {
			return low
		}
		return in
	}

	var (
		// Make sure there are no zeros
		red   = float64(srcnrgba.R) / nrgba64Max
		green = float64(srcnrgba.G) / nrgba64Max
		blue  = float64(srcnrgba.B) / nrgba64Max

		// Apply the previous error
		// clamp pixel to minimum value.  This avoids a black mesh if the input pixel is black.
		// Also, if there is a row of black pixels, the error can build up.  By clamping, negative
		// will not get excessive.  (this consumes the first bright pixel after a string of dark
		// pixels otherwise).
		errorred   = nonneg(red + errcurr[srcx+1].r)
		errorgreen = nonneg(green + errcurr[srcx+1].g)
		errorblue  = nonneg(blue + errcurr[srcx+1].b)

		// apply the new gamma
		newred   = math.Pow(errorred, 1/targetGamma)
		newgreen = math.Pow(errorgreen, 1/targetGamma)
		newblue  = math.Pow(errorblue, 1/targetGamma)

		// bring value up to 0-newMaxValue range
		roundred   = scaleClamp(newred, newMaxValue)
		roundgreen = scaleClamp(newgreen, newMaxValue)
		roundblue  = scaleClamp(newblue, newMaxValue)
	)

	if dither {
		// Undo the gamma transform once more to make the error linear
		var (
			diffred   = errorred - math.Pow(roundred/newMaxValue, targetGamma)
			diffgreen = errorgreen - math.Pow(roundgreen/newMaxValue, targetGamma)
			diffblue  = errorblue - math.Pow(roundblue/newMaxValue, targetGamma)
		)

		errcurr[srcx+2].r += diffred * 7 / 16
		errcurr[srcx+2].g += diffgreen * 7 / 16
		errcurr[srcx+2].b += diffblue * 7 / 16

		errnext[srcx].r += diffred * 3 / 16
		errnext[srcx].g += diffgreen * 3 / 16
		errnext[srcx].b += diffblue * 3 / 16

		errnext[srcx+1].r += diffred * 5 / 16
		errnext[srcx+1].g += diffgreen * 5 / 16
		errnext[srcx+1].b += diffblue * 5 / 16

		errnext[srcx+2].r += diffred * 1 / 16
		errnext[srcx+2].g += diffgreen * 1 / 16
		errnext[srcx+2].b += diffblue * 1 / 16
	}
	println(srcnrgba.A)
	return color.NRGBA{
		R: uint8(roundred),
		G: uint8(roundgreen),
		B: uint8(roundblue),
		A: uint8(srcnrgba.A),
	}
}

func gammaMuxImages(thumbnail, full image.Image, dither, stretch bool) (image.Image, *errchain) {
	noOffsetThumbnailRec := image.Rectangle{
		Max: image.Point{
			X: thumbnail.Bounds().Dx(),
			Y: thumbnail.Bounds().Dy(),
		},
	}

	// linearize before resizing
	linearfull := linearImage(full, sourceGamma)
	// Always resize, regardless of dimensions
	smallfull, xoffset, yoffset := resize(linearfull, noOffsetThumbnailRec, fullScaling, stretch)
	// thumbnailDarkenFactor is a max value that will turn to black after the gamma transform
	darkThumbnail := darkenImage(thumbnail, thumbnailDarkenFactor)
	var errcurr, errnext []dithererr
	errnext = make([]dithererr, smallfull.Bounds().Dx()+2)

	dst := image.NewNRGBA(noOffsetThumbnailRec)

	for srcy := 0; srcy < dst.Bounds().Max.Y; srcy++ {
		for srcx := 0; srcx < dst.Bounds().Max.X; srcx++ {
			dst.SetNRGBA(srcx, srcy, color.NRGBAModel.Convert(darkThumbnail.NRGBA64At(srcx, srcy)).(color.NRGBA))
		}
	}

	var dsty int
	for srcy := smallfull.Bounds().Min.Y; srcy < smallfull.Bounds().Max.Y; srcy++ {
		errcurr = errnext
		errnext = make([]dithererr, smallfull.Bounds().Dx()+2)
		var dstx int
		for srcx := smallfull.Bounds().Min.X; srcx < smallfull.Bounds().Max.X; srcx++ {
			srcnrgba := color.NRGBA64Model.Convert(smallfull.At(srcx, srcy)).(color.NRGBA64)
			newFullPixel := calculateFullPixel(srcx, srcnrgba, dither, errcurr, errnext)
			dst.Set(dstx+xoffset, dsty+yoffset, newFullPixel)
			dstx += fullScaling
		}
		dsty += fullScaling
	}

	return dst, nil
}

func gammaMuxData(thumbnail, full io.Reader, dest io.Writer, dither, stretch bool) *errchain {
	// sadly, Go's own decoder does not handle Gamma properly.  This program shares shame
	// with all the other non-compliant renderers.
	tim, _, err := image.Decode(thumbnail)
	if err != nil {
		return chain(err, "Unable to decode thumbnail")
	}
	fim, _, err := image.Decode(full)
	if err != nil {
		return chain(err, "Unable to decode full")
	}

	dim, ec := gammaMuxImages(tim, fim, dither, stretch)
	if ec != nil {
		return ec
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dim); err != nil {
		return chain(err, "Unable to encode dest PNG")
	}

	headerIndex := bytes.Index(buf.Bytes(), []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R'})
	if headerIndex <= 0 {
		return chain(nil, "PNG missing header")
	}
	headerIndexEnd := headerIndex + 13 + 4 + 4 + 4

	if _, err := dest.Write(buf.Bytes()[:headerIndexEnd]); err != nil {
		return chain(err, "Unable to write PNG header")
	}
	if ec := writeGamaPngChunk(dest, targetGamma); ec != nil {
		return ec
	}
	if _, err := dest.Write(buf.Bytes()[headerIndexEnd:]); err != nil {
		return chain(err, "Unable to write PNG header")
	}

	return nil
}

func writeGamaPngChunk(w io.Writer, gamma float64) *errchain {
	gamaBuf := make([]byte, 4+4+4+4)
	copy(gamaBuf, []byte{0, 0, 0, 4, 'g', 'A', 'M', 'A'})
	binary.BigEndian.PutUint32(gamaBuf[8:12], uint32(math.Round(100000/gamma)))
	crc := crc32.NewIEEE()
	crc.Write(gamaBuf[4:12])
	binary.BigEndian.PutUint32(gamaBuf[12:16], crc.Sum32())
	if _, err := w.Write(gamaBuf); err != nil {
		return chain(err, "Unable to write PNG gAMA chunk")
	}
	return nil
}

func gammaMuxFiles(thumbnail, full, dest string, dither, stretch bool) *errchain {
	tf, err := os.Open(thumbnail)
	if err != nil {
		return chain(err, "Unable to open thumbnail file")
	}
	defer tf.Close()

	ff, err := os.Open(full)
	if err != nil {
		return chain(err, "Unable to open full file")
	}
	defer ff.Close()

	df, err := os.Create(dest)
	if err != nil {
		return chain(err, "Unable create dest file")
	}
	defer df.Close()

	return gammaMuxData(tf, ff, df, dither, stretch)
}

func runHttpServer() {
	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`
      <!doctype html>
      <html>
      <head>
        <meta charset="utf-8">
        <title>Gammux - Gamma Muxer</title>
      </head>
      <body>
      <h1>Gammux - Gamma Muxer</h1>
      <fieldset>
        <form action="/" method="post" enctype="multipart/form-data">
          <dl>
            <dt style="display:inline-block">Thumbnail Image</dt>
            <dd style="display:inline-block"><input type="file" name="thumbnail" /></dd>
          </dl>
          <dl>
            <dt style="display:inline-block">Full Image</dt>
            <dd style="display:inline-block"><input type="file" name="full" /></dd>
          </dl>
          <input type="submit" value="Submit" />
        </form>
      </fieldset>
      </body>
      </html>
      `))
			return
		}
		thumbnail, _, err := r.FormFile("thumbnail")
		if err != nil {
			log.Println(err)
			http.Error(w, "Problem reading thumbnail "+err.Error(), http.StatusBadRequest)
			return
		}
		full, _, err := r.FormFile("full")
		if err != nil {
			log.Println(err)
			http.Error(w, "Problem reading full "+err.Error(), http.StatusBadRequest)
			return
		}
		var dest bytes.Buffer
		if ec := gammaMuxData(thumbnail, full, &dest, *dither, *stretch); ec != nil {
			log.Println(ec)
			http.Error(w, "Problem making image "+ec.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", "attachment; filename=\"merged.png\"")
		w.Write(dest.Bytes())
	}))
	log.Println("Open up your Web Browser to: http://localhost:8080/")
	log.Println(http.ListenAndServe("localhost:8080", nil))
	os.Exit(1)
}

func main() {
	flag.Parse()

	if *thumbnail == "" && *full == "" && *webfallback {
		runHttpServer()
	} else if ec := gammaMuxFiles(*thumbnail, *full, *dest, *dither, *stretch); ec != nil {
		log.Println(ec)
		os.Exit(1)
	}
}
