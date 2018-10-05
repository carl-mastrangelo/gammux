package main

import (
	"bytes"
	"flag"
	"log"
	"net/http"
	"os"

	"./internal"
)

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
		if ec := internal.GammaMuxData(thumbnail, full, &dest, *dither, *stretch); ec != nil {
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
	} else if ec := internal.GammaMuxFiles(*thumbnail, *full, *dest, *dither, *stretch); ec != nil {
		log.Println(ec)
		os.Exit(1)
	}
}
