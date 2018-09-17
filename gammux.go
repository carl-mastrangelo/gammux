package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log"
	"os"

	"golang.org/x/image/draw"
)

const (
	fullScaling = 2
)

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
		"  Use if the Full image doesn't contain text nor is already using few colors" +
		" (such as comics).")

	thumbnail = flag.String("thumbnail", "", "The file path of the Thumbnail(front) image")
	full      = flag.String("full", "", "The file path of the Full(back) image")
	dest      = flag.String("dest", "", "The dest file path of the PNG image")
)

func resizeFull(src image.Image, targetBounds image.Rectangle, stretch bool) (
    image.Image, int, int) {
	scaler := draw.CatmullRom
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
	scaler.Scale(dst, newTargetBounds, src, src.Bounds(), draw.Over, nil)
	return dst, xoffset, yoffset
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
	_, _ = xoffset, yoffset

	return full, nil
}

func gammaMuxData(thumbnail, full io.Reader, dest io.Writer, dither, stretch bool) *errchain {
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
