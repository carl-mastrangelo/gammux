package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"os"

	"golang.org/x/image/draw"
)

const (
	fullScaling = 2
	targetGamma = 1 / .023
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

	thumbnail = flag.String("thumbnail", "", "The file path of the Thumbnail(front) image")
	full      = flag.String("full", "", "The file path of the Full(back) image")
	dest      = flag.String("dest", "", "The dest file path of the PNG image")
)

type setter interface {
	Set(x, y int, c color.Color)
}

func darkenThumbnail(thumbnail image.Image, scale float64) image.Image {
	newThumbnail := image.NewRGBA64(image.Rectangle{
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
			newThumbnail.Set(dstx, dsty, color.RGBA64{
				R: uint16(float64(r) * scale),
				G: uint16(float64(g) * scale),
				B: uint16(float64(b) * scale),
				A: uint16(a),
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
	}

	// Check if the source image is wider than the dest, or narrower.   The odd multiplication avoids
	// casting to float, at the risk of possibly overflow.  Don't use images taller or wider than 32K
	// on 32 bits machines.
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

	dst := image.NewNRGBA(newTargetBounds)
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

	var xoffset, yoffset int
	if full.Bounds() != noOffsetThumbnailRec {
		full, xoffset, yoffset = resizeFull(full, noOffsetThumbnailRec, stretch)
	}
	// thumbnailDarkenFactor is a max value that will turn to black after the gamma transform
	dst := darkenThumbnail(thumbnail, thumbnailDarkenFactor).(*image.RGBA64)
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
			log.Println(srcr, srcg, srcb, srca)
			const maxValue = 0xFFFF
			clamp := func(in float64) float64 {
				if in > maxValue {
					return maxValue
				}
				return in
			}
			var (
				// Make sure there are no zeros
				red   = (float64(srcr) + 1) / (maxValue + 1)
				green = (float64(srcg) + 1) / (maxValue + 1)
				blue  = (float64(srcb) + 1) / (maxValue + 1)

				// Remove the old gamma
				linearred   = math.Pow(red, sourceGamma)
				lineargreen = math.Pow(green, sourceGamma)
				linearblue  = math.Pow(blue, sourceGamma)

				// apply the new gamma
				newred   = math.Pow(linearred, 1/targetGamma)
				newgreen = math.Pow(lineargreen, 1/sourceGamma)
				newblue  = math.Pow(linearblue, 1/sourceGamma)

				// Add error and bring back up to scaled range
				adjustedred   = newred*maxValue + errcurr[srcx+1].r
				adjustedgreen = newgreen*maxValue + errcurr[srcx+1].g
				adjustedblue  = newblue*maxValue + errcurr[srcx+1].b

				roundred   = clamp(math.Round(adjustedred))
				roundgreen = clamp(math.Round(adjustedgreen))
				roundblue  = clamp(math.Round(adjustedblue))
			)

			if dither {
				var (
					diffred   = adjustedred - roundred
					diffgreen = adjustedgreen - roundgreen
					diffblue  = adjustedblue - roundblue
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

			dst.SetRGBA64(dstx+xoffset, dsty+yoffset, color.RGBA64{
				R: uint16(roundred),
				G: uint16(roundgreen),
				B: uint16(roundblue),
				A: uint16(srca),
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

	if err := png.Encode(dest, dim); err != nil {
		return chain(err, "Unable to encode dest PNG")
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

	df, err := os.Create(dest)
	if err != nil {
		return chain(err, "Unable create dest file")
	}
	defer df.Close()

	return gammaMuxData(tf, ff, df, dither, stretch)
}

func main() {
	flag.Parse()

	if ec := gammaMuxFiles(*thumbnail, *full, *dest, *dither, *stretch); ec != nil {
		log.Println(ec)
		os.Exit(1)
	}
}