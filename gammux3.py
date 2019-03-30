#!/usr/bin/python

"""
  This program muxes two images together by splitting them into high and low gamma pieces, and then
  interleaving them.  Several imaging programs don't honor the gamma settings on images when
  resizing them, leading to an incorrect (and amusing) preference for the Thumbnail image.  This is
  called the front image, since typically a thumbnail is shown instead of the full image.  
  
  Browsers, on the other hand, do properly interpret gamma, which means they display a seemingly 
  different image that the thumbnail.  The Full (or background) image is properly gamma corrected 
  down to a range visible by people.  The different treatment by resizing programs (for making
  thumbnails) and browsers allows us to trick a user into clicking on something, but it actually
  being something else.  I don't have an opinion if this is a good idea.
  
  The output image from this program will be a slightly darker, slightly desaturated form of the 
  thumbnail, as well as a much darker, slightly corrupted form of the full image.  Popular websites
  (such as Facebook) are immune to such tricks, and thus the produced image may not work everywhere.
  
  This program can be found at https://github.com/carl-mastrangelo/gammux
"""

import argparse
from PIL import Image

parser = argparse.ArgumentParser(description="Muxes two immages together")
parser.add_argument(
    "--stretch",
    dest="stretch",
    type=bool,
    nargs="?",
    help="If true, stretches the Full(back) image to fit the Thumbnail(front) image." +
        "  If false, the Full image will be scaled proportionally to fit and centered.")
parser.add_argument(
    "--dither",
    dest="dither",
    type=bool,
    nargs="?",
    help="If true, dithers the Full(back) image to hide banding.  Use if the Full image" +
      " doesn't contain text nor is already using few colors (such as comics).")

parser.add_argument(
    "thumbnail",
    type=str,
    help="The file path of the Thumbnail(front) image")
parser.add_argument(
    "full",
    type=str,
    help="The file path of the Full(back) image")
parser.add_argument(
    "dest.png",
    type=str,
    help="The dest file path of the PNG image")

args = parser.parse_args()

thumb = Image.open(args.thumbnail)
full = Image.open(args.full)

stretch = args.stretch  or True
dither = args.dither or False

if stretch:
    full = full.resize((thumb.size[0] // 2, thumb.size[1] // 2), Image.LANCZOS)
    XOFF = 0
    YOFF = 0
else:
    if full.size[0] *1.0 / full.size[1] > thumb.size[0] * 1.0 / thumb.size[1]:
        full = full.resize(
            (thumb.size[0] // 2, int(full.size[1] / 2.0 / full.size[0] * thumb.size[0])),
            Image.LANCZOS)
        XOFF = 0
        YOFF = (thumb.size[1] - full.size[1]*2) / 2
    else:
        full = full.resize(
            (int(full.size[0] / 2.0 / full.size[1] * thumb.size[1]), thumb.size[1] // 2),
            Image.LANCZOS)
        XOFF = (thumb.size[0] - full.size[0] * 2) / 2
        YOFF = 0

out = Image.new("RGB", thumb.size)

for y in range(thumb.size[1]):
    for x in range(thumb.size[0]):
        src = thumb.getpixel((x, y))
        out.putpixel((x, y), (int(src[0]*.9), int(src[1]*.9), int(src[2]*.9)))

errcurr = []
errnext = []
for x in range(full.size[0] + 2):
    errnext.append([0.0, 0.0, 0.0])

for y in range(full.size[1]):
    errcurr = errnext
    errnext = []
    for x in range(full.size[0] + 2):
        errnext.append([0.0, 0.0, 0.0])
    for x in range(full.size[0]):
        src = full.getpixel((x, y))
        # bright = src[0]*.299 + src[1]*.587 + src[2]*.114
        r, g, b = src[0], src[1], src[2]
        rf, gf, bf = (r+1) / 256.0, (g+1) / 256.0, (b+1) / 256.0
        rf, gf, bf = rf ** (1/.454545), gf ** (1/.454545), bf ** (1/.454545), # undo normal gamma

        rf, gf, bf = rf **(.01), gf **(.01), bf **(.01) # redo new gamma
        adjr, adjg, adjb = rf*255+errcurr[x+1][0], gf*255+errcurr[x+1][1], bf*255+errcurr[x+1][2]
        roundr, roundg, roundb = min(round(adjr), 255), min(round(adjg), 255), min(round(adjb), 255)
        diffr, diffg, diffb = adjr - roundr, adjg - roundg, adjb - roundb

        if dither:
            # The error is slightly incorrect for edges but meh.
            # Also, the error is being treated as linear, but it's actually exponential.  Meh.
            errcurr[x+2][0] += diffr*7/16
            errcurr[x+2][1] += diffg*7/16
            errcurr[x+2][2] += diffb*7/16

            errnext[x][0] += diffr*3/16
            errnext[x][1] += diffg*3/16
            errnext[x][2] += diffb*3/16

            errnext[x+1][0] += diffr*5/16
            errnext[x+1][1] += diffg*5/16
            errnext[x+1][2] += diffb*5/16

            errnext[x+2][0] += diffr*1/16
            errnext[x+2][1] += diffg*1/16
            errnext[x+2][2] += diffb*1/16

        out.putpixel((x*2+XOFF, y*2+YOFF), (int(roundr), int(roundg), int(roundb)))

dest = getattr(args, "dest.png")
out.save(dest)

