import { describe, it, expect } from "vitest"
import { isVolumeRoot } from "./volumeRoot"

// Backslashes are doubled, not raw-quoted: a raw template literal cannot end
// in a backslash, which is exactly the shape a drive root has.
describe("isVolumeRoot", () => {
  it("erkennt Windows-Laufwerks-Roots in allen Schreibweisen", () => {
    expect(isVolumeRoot("D:\\")).toBe(true)
    expect(isVolumeRoot("D:/")).toBe(true)
    expect(isVolumeRoot("d:")).toBe(true)
    expect(isVolumeRoot("  C:\\  ")).toBe(true)
  })

  it("laesst Verzeichnisse innerhalb des Laufwerks in Ruhe", () => {
    expect(isVolumeRoot("D:\\PrivaSync")).toBe(false)
    expect(isVolumeRoot("D:\\Backup\\Fotos")).toBe(false)
    expect(isVolumeRoot("C:/Users/user/Sync")).toBe(false)
  })

  it("deckt POSIX-Root und blanke UNC-Freigaben ab", () => {
    expect(isVolumeRoot("/")).toBe(true)
    expect(isVolumeRoot("\\\\server\\share")).toBe(true)
    expect(isVolumeRoot("\\\\server\\share\\unterordner")).toBe(false)
    expect(isVolumeRoot("/home/user/sync")).toBe(false)
  })

  it("behandelt Leereingaben als kein Laufwerks-Root", () => {
    expect(isVolumeRoot("")).toBe(false)
    expect(isVolumeRoot("   ")).toBe(false)
  })
})
