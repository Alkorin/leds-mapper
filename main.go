package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/blackjack/webcam"
)

type WebCam struct {
	cam *webcam.Webcam
}

func NewWebCam(path string) (*WebCam, error) {
	cam, err := webcam.Open("/dev/video1") // Open webcam
	if err != nil {
		return nil, err
	}

	err = cam.StartStreaming()
	if err != nil {
		return nil, err
	}

	return &WebCam{cam: cam}, nil
}

func (c *WebCam) Close() {
	c.cam.Close()
}

func (c *WebCam) GetFrame() (*image.RGBA, error) {
	err := c.cam.WaitForFrame(1000)
	if err != nil {
		return nil, err
	}

	frame, err := c.cam.ReadFrame()
	if err != nil {
		return nil, err
	}

	image := image.NewRGBA(image.Rect(0, 0, 480, 640))
	offset := 0
	//for x := 0; x < 480; x++ {
	for x := 479; x >= 0; x-- {
		//for y := 639; y >= 0; y-- {
		for y := 0; y < 640; y++ {
			image.SetRGBA(x, y, color.RGBA{frame[offset], frame[offset+1], frame[offset+2], 0})
			offset += 3
		}
	}
	return image, nil
}

func diffsat(a, b uint32) uint8 {
	if b > a {
		return 0
	} else {
		return uint8(a - b)
	}
}

func drawCircle(img draw.Image, x0, y0, r int, c color.Color) {
	x, y, dx, dy := r-1, 0, 1, 1
	err := dx - (r * 2)

	for x > y {
		img.Set(x0+x, y0+y, c)
		img.Set(x0+y, y0+x, c)
		img.Set(x0-y, y0+x, c)
		img.Set(x0-x, y0+y, c)
		img.Set(x0-x, y0-y, c)
		img.Set(x0-y, y0-x, c)
		img.Set(x0+y, y0-x, c)
		img.Set(x0+x, y0-y, c)

		if err <= 0 {
			y++
			err += dy
			dy += 2
		}
		if err > 0 {
			x--
			dx += 2
			err += dx - (r * 2)
		}
	}
}

func Handler(cam *WebCam) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()

		var coordinates []image.Point

		currentLed, err := strconv.Atoi(r.Form.Get("currentLed"))
		if err != nil {
			currentLed = 0
		}

		if r.Form.Get("coordinates") != "" {
			json.Unmarshal([]byte(r.Form.Get("coordinates")), &coordinates)
		} else {
			// Initialize buffer
			coordinates = make([]image.Point, 200)
		}

		if r.Form.Get("submit") == "Submit" {
			currentLed++
		}

		if r.Form.Get("submit") == "Skip" {
			coordinates[currentLed].X = -1
			coordinates[currentLed].Y = -1
			currentLed++
		}

		if currentLed == len(coordinates) {
			v, _ := json.Marshal(coordinates)
			w.Write(v)
			return
		}

		// Init UDP socket
		raddr, err := net.ResolveUDPAddr("udp", "192.168.3.156:8080")
		if err != nil {
			return
		}

		conn, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send all black
		dataLed := make([]byte, 600)
		conn.Write(dataLed)

		// Wait
		time.Sleep(500 * time.Millisecond)

		// Grab a frame
		referenceFrame, err := cam.GetFrame()
		if err != nil {
			log.Print(err)
			return
		}

		// Make one led bright
		dataLed[3*currentLed] = 255
		dataLed[3*currentLed+1] = 255
		dataLed[3*currentLed+2] = 255
		conn.Write(dataLed)

		// Wait ~50ms
		time.Sleep(500 * time.Millisecond)

		// Grab a new frame
		newFrame, err := cam.GetFrame()
		if err != nil {
			log.Print(err)
			return
		}

		// Find brighest diff
		var brightestPoint image.Point
		var brightestValue uint8

		diffFrame := image.NewRGBA(image.Rect(0, 0, 480, 640))
		for x := 0; x < 480; x++ {
			for y := 0; y < 640; y++ {
				refR, refG, refB, _ := referenceFrame.At(x, y).RGBA()
				newR, newG, newB, _ := newFrame.At(x, y).RGBA()
				diffR, diffG, diffB := diffsat(newR, refR), diffsat(newG, refG), diffsat(newB, refB)

				// Compute brightness
				lum, _, _ := color.RGBToYCbCr(diffR, diffG, diffB)
				if lum > brightestValue {
					brightestPoint = image.Point{x, y}
					brightestValue = lum
				}

				diffFrame.SetRGBA(x, y, color.RGBA{diffR, diffG, diffB, 0})
			}
		}

		// Draw red circle around found pixel
		drawCircle(newFrame, brightestPoint.X, brightestPoint.Y, 10, color.RGBA{255, 0, 0, 0})
		drawCircle(diffFrame, brightestPoint.X, brightestPoint.Y, 10, color.RGBA{255, 0, 0, 0})
		coordinates[currentLed].X = brightestPoint.X
		coordinates[currentLed].Y = brightestPoint.Y

		tmpl := `
<!DOCTYPE html>
<html>
	<body>
	<table>
		<tr>
			<th>Reference Frame</th>
			<th>New Frame</th>
			<th>Diff Frame</th>
		</tr>
		<tr>
			<td><img src="data:image/png;base64,{{.refFrame}}"></img></td>
			<td><img src="data:image/png;base64,{{.newFrame}}"></img></td>
			<td><img src="data:image/png;base64,{{.diffFrame}}"></img></td>
		</tr>
		</table>
		Found coordinates : X={{.brightestPoint.X}}, Y={{.brightestPoint.Y}}
		<form method="POST">
			<button type="submit" name="submit" value="Submit">Validate</button>
			<button type="submit" name="submit" value="Retry">Retry</button>
			<button type="submit" name="submit" value="Skip">Skip</button>			
			<input type="hidden" name="currentLed" value="{{.currentLed}}"/>
			<input type="hidden" name="coordinates" value="{{.coordinates}}"/>
		</form>
		{{.coordinates}}
	</body>
</html>`

		t, _ := template.New("webpage").Parse(tmpl)

		var refFrameJPGBuffer bytes.Buffer
		jpeg.Encode(&refFrameJPGBuffer, referenceFrame, nil)

		var newFrameJPGBuffer bytes.Buffer
		jpeg.Encode(&newFrameJPGBuffer, newFrame, nil)

		var diffFrameJPGBuffer bytes.Buffer
		jpeg.Encode(&diffFrameJPGBuffer, diffFrame, nil)

		currentCoordinates, _ := json.Marshal(coordinates)

		t.Execute(w, map[string]interface{}{
			"refFrame":       base64.StdEncoding.EncodeToString(refFrameJPGBuffer.Bytes()),
			"newFrame":       base64.StdEncoding.EncodeToString(newFrameJPGBuffer.Bytes()),
			"diffFrame":      base64.StdEncoding.EncodeToString(diffFrameJPGBuffer.Bytes()),
			"brightestPoint": brightestPoint,
			"currentLed":     currentLed,
			"coordinates":    string(currentCoordinates),
		})
	}
}

func main() {
	cam, err := NewWebCam("/dev/video1")
	if err != nil {
		panic(err.Error())
	}
	defer cam.Close()

	http.HandleFunc("/find", Handler(cam))
	http.ListenAndServe(":8081", nil)
}
