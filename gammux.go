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
	targetGamma = 1 / .02
	sourceGamma = 2.2 // this is the common default.  Use this since Go doesn't expose it.
)

var thumbnailDarkenFactor = math.Pow( /*darkest pixel=*/ math.Nextafter(0.5, 0)/255, 1/targetGamma)

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

	dither = flag.Bool("dither", false, "If true, dithers the Full(back) image to hide banding."+
		"  Use if the Full image doesn't contain text nor is already using few colors"+
		" (such as comics).")

	thumbnail   = flag.String("thumbnail", "", "The file path of the Thumbnail(front) image")
	full        = flag.String("full", "", "The file path of the Full(back) image")
	dest        = flag.String("dest", "", "The dest file path of the PNG image")
	webfallback = flag.Bool(
		"webfallback", true, "If true, enable a web UI fallback at http://localhost:8080/")
)

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
			const max = 0xFFFF
			nrgba64 := color.NRGBA64Model.Convert(srcim.At(srcx, srcy)).(color.NRGBA64)
			nrgba64.R = uint16(max * math.Pow(float64(nrgba64.R)/max, gamma))
			nrgba64.G = uint16(max * math.Pow(float64(nrgba64.G)/max, gamma))
			nrgba64.B = uint16(max * math.Pow(float64(nrgba64.B)/max, gamma))
			// Alpha is not affected
			dstim.SetNRGBA64(dstx, dsty, nrgba64)
			dstx++
		}
		dsty++
	}
	return dstim
}

func darkenThumbnail(thumbnail image.Image, scale float64) image.Image {
	newThumbnail := image.NewRGBA(image.Rectangle{
		Max: image.Point{
			X: thumbnail.Bounds().Dx(),
			Y: thumbnail.Bounds().Dy(),
		},
	})
	var dsty int
	for srcy := thumbnail.Bounds().Min.Y; srcy < thumbnail.Bounds().Max.Y; srcy++ {
		var dstx int
		for srcx := thumbnail.Bounds().Min.X; srcx < thumbnail.Bounds().Max.X; srcx++ {
			r, g, b, a := thumbnail.At(srcx, srcy).RGBA()
			newThumbnail.SetRGBA(dstx, dsty, color.RGBA{
				R: uint8(float64(r>>8) * scale),
				G: uint8(float64(g>>8) * scale),
				B: uint8(float64(b>>8) * scale),
				A: uint8(a >> 8),
			})
			dstx++
		}
		dsty++
	}
	return newThumbnail
}

func resizeFull(src image.Image, targetBounds image.Rectangle, stretch bool) (
	image.Image, int, int) {
	var xoffset, yoffset int
	var newTargetBounds image.Rectangle
	if stretch {
		newTargetBounds = image.Rectangle{
			Max: image.Point{
				X: targetBounds.Dx() / fullScaling,
				Y: targetBounds.Dy() / fullScaling,
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
					X: targetBounds.Dx() / fullScaling,
					Y: src.Bounds().Dy() * targetBounds.Dx() / src.Bounds().Dx() / fullScaling,
				},
			}
			yoffset = (targetBounds.Dy() - newTargetBounds.Dy()*fullScaling) / 2
		} else {
			// source image is narrower.
			newTargetBounds = image.Rectangle{
				Max: image.Point{
					X: src.Bounds().Dx() * targetBounds.Dy() / src.Bounds().Dy() / fullScaling,
					Y: targetBounds.Dy() / fullScaling,
				},
			}
			xoffset = (targetBounds.Dx() - newTargetBounds.Dx()*fullScaling) / 2
		}
	}

	dst := image.NewRGBA(newTargetBounds)
	scaler := draw.CatmullRom
	scaler.Scale(dst, newTargetBounds, src, src.Bounds(), draw.Over, nil)
	return dst, xoffset, yoffset
}

type dithererr struct {
	r, g, b float64
}

func gammaMuxImages(thumbnail, full image.Image, dither, stretch bool) (image.Image, *errchain) {
	noOffsetThumbnailRec := image.Rectangle{
		Max: image.Point{
			X: thumbnail.Bounds().Dx(),
			Y: thumbnail.Bounds().Dy(),
		},
	}

	// linearize before resizing
	full = linearImage(full, sourceGamma)
	// Always resize, regardless of dimensions
	full, xoffset, yoffset := resizeFull(full, noOffsetThumbnailRec, stretch)
	// thumbnailDarkenFactor is a max value that will turn to black after the gamma transform
	dst := darkenThumbnail(thumbnail, thumbnailDarkenFactor).(*image.RGBA)
	if dst.Bounds() != noOffsetThumbnailRec {
		panic("Bad bounds")
	}
	var errcurr, errnext []dithererr
	errnext = make([]dithererr, full.Bounds().Dx()+2)

	var dsty int
	for srcy := full.Bounds().Min.Y; srcy < full.Bounds().Max.Y; srcy++ {
		errcurr = errnext
		errnext = make([]dithererr, full.Bounds().Dx()+2)
		var dstx int
		for srcx := full.Bounds().Min.X; srcx < full.Bounds().Max.X; srcx++ {
			srcr, srcg, srcb, srca := full.At(srcx, srcy).RGBA()
			const oldMaxValue = 0xFFFF
			const newMaxValue = 0xFF
			clamp := func(in float64) float64 {
				if in > newMaxValue {
					return newMaxValue
				}
				return in
			}
			nonneg := func(in float64) float64 {
				if in <= 0 {
					return 1.0 / newMaxValue
				}
				return in
			}
			var (
				// Make sure there are no zeros
				red   = (float64(srcr) + 1) / (oldMaxValue + 1)
				green = (float64(srcg) + 1) / (oldMaxValue + 1)
				blue  = (float64(srcb) + 1) / (oldMaxValue + 1)

				// Apply the previous error
				errorred   = red + errcurr[srcx+1].r
				errorgreen = green + errcurr[srcx+1].g
				errorblue  = blue + errcurr[srcx+1].b

				// apply the new gamma
				newred   = math.Pow(nonneg(errorred), 1/targetGamma)
				newgreen = math.Pow(nonneg(errorgreen), 1/targetGamma)
				newblue  = math.Pow(nonneg(errorblue), 1/targetGamma)

				// Add error and bring back up to scaled range
				adjustedred   = newred * newMaxValue
				adjustedgreen = newgreen * newMaxValue
				adjustedblue  = newblue * newMaxValue

				roundred   = clamp(math.Round(adjustedred))
				roundgreen = clamp(math.Round(adjustedgreen))
				roundblue  = clamp(math.Round(adjustedblue))
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

			dst.SetRGBA(dstx+xoffset, dsty+yoffset, color.RGBA{
				R: uint8(roundred),
				G: uint8(roundgreen),
				B: uint8(roundblue),
				A: uint8(srca >> 8),
			})
			dstx += fullScaling
		}
		dsty += fullScaling
	}

	return dst, nil
}

func gammaMuxData(thumbnail, full io.Reader, dest io.Writer, dither, stretch bool) *errchain {
	// sadly, Go's own decoder does not handle Gamma properly.  This program shares the shame
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

	gamaBuf := make([]byte, 4+4+4+4)
	copy(gamaBuf, []byte{0, 0, 0, 4, 'g', 'A', 'M', 'A'})
	binary.BigEndian.PutUint32(gamaBuf[8:12], uint32(100000/targetGamma))
	crc := crc32.NewIEEE()
	crc.Write(gamaBuf[4:12])
	binary.BigEndian.PutUint32(gamaBuf[12:16], crc.Sum32())
	if _, err := dest.Write(gamaBuf); err != nil {
		return chain(err, "Unable to write PNG gAMA chunk")
	}
	if _, err := dest.Write(buf.Bytes()[headerIndexEnd:]); err != nil {
		return chain(err, "Unable to write PNG header")
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
	log.Println("Listening on http://localhost:8080/")
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
