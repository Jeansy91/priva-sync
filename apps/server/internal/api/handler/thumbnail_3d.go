package handler

import (
	"archive/zip"
	"errors"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/fogleman/fauxgl"
	"github.com/google/uuid"
)

// extractZipThumb sucht in einem ZIP-Container (z.B. 3MF) nach einem
// eingebetteten PNG-Thumbnail. Slicer wie PrusaSlicer/Bambu legen das
// Projektbild typischerweise unter "Metadata/" ab. Liefert nil, wenn keins
// gefunden — der Caller fällt dann auf den Software-Renderer zurück.
func extractZipThumb(s storageReader, userID uuid.UUID, rootID, filePath string) (image.Image, error) {
	tmp, err := os.CreateTemp("", "priva-3mf-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err := s.ServeFile(userID, rootID, filePath, tmp); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	_ = tmp.Close()

	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	// Heuristik: bevorzugt "Metadata/plate_*.png" (Bambu) oder
	// "Metadata/thumbnail.png" (PrusaSlicer/Cura). Sonst erstes PNG/JPEG.
	var best *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.ToLower(f.Name)
		if !strings.HasSuffix(name, ".png") && !strings.HasSuffix(name, ".jpg") && !strings.HasSuffix(name, ".jpeg") {
			continue
		}
		if strings.Contains(name, "metadata/") && (strings.Contains(name, "thumbnail") || strings.Contains(name, "plate")) {
			best = f
			break
		}
		if best == nil {
			best = f
		}
	}
	if best == nil {
		return nil, errors.New("no embedded thumbnail in container")
	}
	rc, err := best.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	img, _, err := image.Decode(rc)
	return img, err
}

// renderMesh3D rendert STL/OBJ/PLY via fauxgl (pure-Go Software-Rasterizer)
// zu einer NRGBA-Image. Heuristische Kamera so positioniert, dass das Modell
// formatfüllend dargestellt wird.
func renderMesh3D(s storageReader, userID uuid.UUID, rootID, filePath string, size int) (image.Image, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	tmp, err := os.CreateTemp("", "priva-3d-*"+ext)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if err := s.ServeFile(userID, rootID, filePath, tmp); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	_ = tmp.Close()

	var mesh *fauxgl.Mesh
	switch ext {
	case ".stl":
		mesh, err = fauxgl.LoadSTL(tmp.Name())
	case ".obj":
		mesh, err = fauxgl.LoadOBJ(tmp.Name())
	case ".ply":
		mesh, err = fauxgl.LoadPLY(tmp.Name())
	default:
		return nil, errors.New("unsupported 3d format for software render")
	}
	if err != nil {
		return nil, err
	}
	mesh.BiUnitCube()
	// STL hat nur Face-Normalen — ohne Smooth-Pass wirken organische Meshes
	// kantig oder bleiben durch fauxgls Phong-Shader nahezu unbeleuchtet.
	mesh.SmoothNormalsThreshold(fauxgl.Radians(30))

	// Kamera-Distanz so wählen, dass die Bounding-Sphere des Meshes
	// formatfüllend in den FOV passt. Sonst clippt das Modell am Rand.
	box      := mesh.BoundingBox()
	center   := box.Center()
	radius   := box.Size().Length() / 2.0
	const (
		fovy   = 30.0
		margin = 1.15
	)
	distance := radius/math.Sin(fauxgl.Radians(fovy/2)) * margin
	dir   := fauxgl.V(-3, 1, -0.75).Normalize()
	eye   := center.Add(dir.MulScalar(distance))
	up    := fauxgl.V(0, 1, 0)
	light := fauxgl.V(-0.75, 1, 0.25).Normalize()
	// Near/Far so dimensioniert, dass die gesamte Bounding-Sphere zwischen
	// beiden Ebenen liegt, mit etwas Luft für Rundungen.
	near := math.Max(0.01, distance-radius*1.5)
	far  := distance + radius*1.5

	ctx := fauxgl.NewContext(size, size)
	// Voll transparenter Hintergrund — der Thumbnail-Handler erkennt Alpha
	// und kodiert das Resultat als PNG, sodass die UI das Modell direkt auf
	// jeden Untergrund legen kann.
	ctx.ClearColorBufferWith(fauxgl.Color{R: 0, G: 0, B: 0, A: 0})
	aspect := 1.0
	// Reihenfolge ist wichtig: erst View (LookAt), dann Projection links
	// auf-multiplizieren — `m.Perspective(...)` macht genau das (P · m).
	matrix := fauxgl.LookAt(eye, center, up).Perspective(fovy, aspect, near, far)
	shader := fauxgl.NewPhongShader(matrix, light, eye)
	shader.ObjectColor   = fauxgl.HexColor("4F8BD8")
	shader.AmbientColor  = fauxgl.Gray(0.3)
	shader.DiffuseColor  = fauxgl.Gray(0.9)
	shader.SpecularColor = fauxgl.Gray(0.25)
	shader.SpecularPower = 50
	ctx.Shader = shader
	ctx.DrawMesh(mesh)
	return ctx.Image(), nil
}

