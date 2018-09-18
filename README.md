# Gammux
A Gamma muxing tool

This tool merges two pictures together by splitting them into high 
and low brightness images.   The lighter image is scaled based on a [custom
gamma](http://www.libpng.org/pub/png/spec/1.2/PNG-Chunks.html#C.gAMA]) amount, 
which most programs don't support.   However, browsers typically do support 
gamma, which affords the ability to make an image appear differently based
on where it is viewed.

To run:

```bash
go run gammux.go -thumbnail ./fine.jpg  -full ./notfine.png  -dest mux.png -dither=true
```

This will produce ![mux.png](https://github.com/carl-mastrangelo/gammux/raw/master/merged.png "Muxed").





