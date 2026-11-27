import { useEffect, useRef, useState } from 'react'
import { AlertCircle, Loader2 } from 'lucide-react'
import { useTranslation } from '@privasync/i18n'

// ModelViewer rendert 3D-Modelle (STL, 3MF, OBJ, GLTF/GLB, PLY, FBX) per
// three.js. Three.js und die Loader werden dynamisch importiert, damit der
// Initial-Bundle frei vom 600 KB-Klotz bleibt — Kosten entstehen erst beim
// ersten Öffnen eines 3D-Files.
export function ModelViewer({ blob, name }: { blob: Blob; name: string }) {
  const { t } = useTranslation()
  const mountRef = useRef<HTMLDivElement | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const mount = mountRef.current
    if (!mount) return
    let disposed = false
    let cleanup: (() => void) | null = null

    ;(async () => {
      try {
        const THREE = await import('three')
        const { OrbitControls } = await import('three/examples/jsm/controls/OrbitControls.js')

        const buf = await blob.arrayBuffer()
        if (disposed) return

        const obj = await loadByExtension(THREE, name, buf)
        if (disposed) return

        const width  = mount.clientWidth
        const height = mount.clientHeight
        const scene  = new THREE.Scene()
        scene.background = null

        const camera = new THREE.PerspectiveCamera(45, width / height, 0.1, 10000)
        const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true })
        renderer.setPixelRatio(window.devicePixelRatio)
        renderer.setSize(width, height)
        mount.appendChild(renderer.domElement)

        // Beleuchtung — Ambient + Hemisphere + drei Directional für gleichmäßige
        // Helligkeit von allen Seiten, damit auch schattige Flächen lesbar sind.
        scene.add(new THREE.AmbientLight(0xffffff, 0.6))
        scene.add(new THREE.HemisphereLight(0xffffff, 0x666666, 1.6))
        const dir1 = new THREE.DirectionalLight(0xffffff, 1.6)
        dir1.position.set(5, 10, 7.5)
        scene.add(dir1)
        const dir2 = new THREE.DirectionalLight(0xffffff, 1.0)
        dir2.position.set(-5, -10, -7.5)
        scene.add(dir2)
        const dir3 = new THREE.DirectionalLight(0xffffff, 0.8)
        dir3.position.set(0, 5, -10)
        scene.add(dir3)

        scene.add(obj)

        // Auto-Fit: Bounding-Box ermitteln, Kamera so positionieren, dass das
        // Modell sichtbar mittig zentriert ist.
        const box    = new THREE.Box3().setFromObject(obj)
        const size   = box.getSize(new THREE.Vector3()).length()
        const center = box.getCenter(new THREE.Vector3())
        obj.position.x -= center.x
        obj.position.y -= center.y
        obj.position.z -= center.z

        camera.position.set(0, 0, size * 1.4)
        camera.near = size / 100
        camera.far  = size * 100
        camera.updateProjectionMatrix()

        const controls = new OrbitControls(camera, renderer.domElement)
        controls.target.set(0, 0, 0)
        controls.enableDamping = true
        controls.dampingFactor = 0.08
        controls.update()

        const onResize = () => {
          const w = mount.clientWidth
          const h = mount.clientHeight
          camera.aspect = w / h
          camera.updateProjectionMatrix()
          renderer.setSize(w, h)
        }
        const ro = new ResizeObserver(onResize)
        ro.observe(mount)

        let raf = 0
        const tick = () => {
          controls.update()
          renderer.render(scene, camera)
          raf = requestAnimationFrame(tick)
        }
        tick()
        setLoading(false)

        cleanup = () => {
          cancelAnimationFrame(raf)
          ro.disconnect()
          controls.dispose()
          renderer.dispose()
          if (renderer.domElement.parentNode) {
            renderer.domElement.parentNode.removeChild(renderer.domElement)
          }
          scene.traverse((node: any) => {
            if (node.geometry?.dispose) node.geometry.dispose()
            if (node.material) {
              const mats = Array.isArray(node.material) ? node.material : [node.material]
              mats.forEach((m: any) => m.dispose?.())
            }
          })
        }
      } catch (err) {
        if (!disposed) {
          setError(t('files.modelLoadFailed'))
          setLoading(false)
        }
        // eslint-disable-next-line no-console
        console.error('ModelViewer load failed', err)
      }
    })()

    return () => {
      disposed = true
      cleanup?.()
    }
  }, [blob, name, t])

  return (
    <div className="relative z-10 h-[95%] w-[95vw] bg-black/40 rounded-sm">
      <div ref={mountRef} className="absolute inset-0" />
      {loading && !error && (
        <div className="absolute inset-0 flex items-center justify-center gap-2 text-white/80">
          <Loader2 className="h-6 w-6 animate-spin" />
          <span className="text-sm">{t('files.modelLoading')}</span>
        </div>
      )}
      {error && (
        <div className="absolute inset-0 flex items-center justify-center gap-2 text-destructive">
          <AlertCircle className="h-4 w-4" />
          <span className="text-sm">{error}</span>
        </div>
      )}
    </div>
  )
}

// Liest --primary aus dem CSS-Root (Tailwind-Format "H S% L%") und liefert
// die Farbe als THREE.Color — nur für Formate ohne embedded Materials
// (STL, PLY); andere behalten ihre originalen Texturen.
function primaryColor(THREE: typeof import('three')): InstanceType<typeof THREE.Color> {
  const c = new THREE.Color(0xb0b8c0)
  if (typeof window === 'undefined') return c
  const raw = getComputedStyle(document.documentElement).getPropertyValue('--primary').trim()
  if (!raw) return c
  try { c.setStyle(`hsl(${raw.replace(/\s+/g, ',')})`) } catch { /* fallback */ }
  return c
}

async function loadByExtension(THREE: typeof import('three'), name: string, buf: ArrayBuffer): Promise<any> {
  const ext = name.toLowerCase().split('.').pop()
  switch (ext) {
    case 'stl': {
      const { STLLoader } = await import('three/examples/jsm/loaders/STLLoader.js')
      const geom = new STLLoader().parse(buf)
      geom.computeVertexNormals()
      const mat = new THREE.MeshStandardMaterial({ color: primaryColor(THREE), metalness: 0.1, roughness: 0.7 })
      return new THREE.Mesh(geom, mat)
    }
    case '3mf': {
      const { ThreeMFLoader } = await import('three/examples/jsm/loaders/3MFLoader.js')
      return new ThreeMFLoader().parse(buf)
    }
    case 'obj': {
      const { OBJLoader } = await import('three/examples/jsm/loaders/OBJLoader.js')
      const text = new TextDecoder().decode(buf)
      return new OBJLoader().parse(text)
    }
    case 'glb':
    case 'gltf': {
      const { GLTFLoader } = await import('three/examples/jsm/loaders/GLTFLoader.js')
      return await new Promise<any>((resolve, reject) => {
        new GLTFLoader().parse(buf, '', (g: any) => resolve(g.scene), reject)
      })
    }
    case 'ply': {
      const { PLYLoader } = await import('three/examples/jsm/loaders/PLYLoader.js')
      const geom = new PLYLoader().parse(buf)
      geom.computeVertexNormals()
      const mat = new THREE.MeshStandardMaterial({ color: primaryColor(THREE), metalness: 0.1, roughness: 0.7, vertexColors: !!geom.getAttribute('color') })
      return new THREE.Mesh(geom, mat)
    }
    case 'fbx': {
      const { FBXLoader } = await import('three/examples/jsm/loaders/FBXLoader.js')
      return new FBXLoader().parse(buf, '')
    }
    case 'dae': {
      const { ColladaLoader } = await import('three/examples/jsm/loaders/ColladaLoader.js')
      const text = new TextDecoder().decode(buf)
      const parsed = new ColladaLoader().parse(text, '')
      return parsed?.scene ?? parsed
    }
    case 'usdz': {
      const { USDZLoader } = await import('three/examples/jsm/loaders/USDZLoader.js')
      return new USDZLoader().parse(buf as any)
    }
    default:
      throw new Error('unsupported model: ' + ext)
  }
}
