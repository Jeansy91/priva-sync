import { describe, it, expect } from 'vitest'
import { previewKindFor, mimeForName } from './previewKind'

describe('previewKindFor', () => {
  it('erkennt Bilder', () => {
    for (const n of ['a.jpg', 'b.JPEG', 'c.png', 'd.gif', 'e.webp', 'f.svg', 'g.avif']) {
      expect(previewKindFor(n)).toBe('image')
    }
  })

  it('erkennt PDF', () => {
    expect(previewKindFor('doc.pdf')).toBe('pdf')
    expect(previewKindFor('DOC.PDF')).toBe('pdf')
  })

  it('erkennt Video', () => {
    for (const n of ['m.mp4', 'm.webm', 'm.ogv', 'm.mov']) {
      expect(previewKindFor(n)).toBe('video')
    }
  })

  it('erkennt 3D-Modelle', () => {
    for (const n of ['part.stl', 'PART.STL', 'a.3mf', 'b.obj', 'c.gltf', 'd.glb', 'e.ply', 'f.fbx']) {
      expect(previewKindFor(n)).toBe('model3d')
    }
  })

  it('erkennt Audio', () => {
    for (const n of ['s.mp3', 's.wav', 's.ogg', 's.flac', 's.m4a']) {
      expect(previewKindFor(n)).toBe('audio')
    }
  })

  it('liefert null für Unbekanntes / ohne Extension', () => {
    expect(previewKindFor('readme')).toBeNull()
    expect(previewKindFor('')).toBeNull()
  })

  it('erkennt Office-Dokumente (OOXML/ODF/Alt-Binaer, ADR-0049)', () => {
    const names = [
      'report.docx', 'sheet.XLSX', 'deck.pptx',
      'text.odt', 'calc.ods', 'slides.odp',
      'vorlage.ott', 'vorlage.ots', 'vorlage.otp',
      'legacy.doc', 'legacy.xls', 'legacy.ppt',
      'makro.docm', 'makro.xlsm', 'makro.pptm',
      'brief.rtf', 'flat.fodt', 'star.sxw',
    ]
    for (const n of names) expect(previewKindFor(n)).toBe('office')
  })

  it('routet Text- und PDF-Formate nicht an den Dokumentenserver', () => {
    // Der Dokumentenserver koennte sie oeffnen — die App hat dafuer aber
    // eigene Viewer (Monaco-Editor, PDF-Vorschau, EPUB).
    expect(previewKindFor('notes.txt')).toBeNull()
    expect(previewKindFor('data.csv')).toBeNull()
    expect(previewKindFor('readme.md')).toBeNull()
    expect(previewKindFor('scan.pdf')).toBe('pdf')
    expect(previewKindFor('book.epub')).toBe('epub')
  })
})

describe('mimeForName', () => {
  it('liefert exakten MIME für bekannte Typen', () => {
    expect(mimeForName('a.svg')).toBe('image/svg+xml')
    expect(mimeForName('a.png')).toBe('image/png')
    expect(mimeForName('a.jpg')).toBe('image/jpeg')
    expect(mimeForName('a.JPEG')).toBe('image/jpeg')
    expect(mimeForName('a.gif')).toBe('image/gif')
    expect(mimeForName('a.webp')).toBe('image/webp')
    expect(mimeForName('a.avif')).toBe('image/avif')
    expect(mimeForName('doc.pdf')).toBe('application/pdf')
    expect(mimeForName('m.mp4')).toBe('video/mp4')
    expect(mimeForName('m.webm')).toBe('video/webm')
    expect(mimeForName('s.mp3')).toBe('audio/mpeg')
    expect(mimeForName('s.wav')).toBe('audio/wav')
    expect(mimeForName('s.flac')).toBe('audio/flac')
  })

  it('liefert null für völlig Unbekanntes', () => {
    expect(mimeForName('a.unknown_ext_zzz')).toBeNull()
    expect(mimeForName('readme')).toBeNull()
  })

  it('liefert MIME für DOCX/XLSX (haben inzwischen eigene Vorschau, ADR-0027)', () => {
    expect(mimeForName('a.xlsx')).toBe('application/vnd.openxmlformats-officedocument.spreadsheetml.sheet')
    expect(mimeForName('a.docx')).toBe('application/vnd.openxmlformats-officedocument.wordprocessingml.document')
  })

  it('liefert MIME für ODF- und Alt-Office-Dateien', () => {
    expect(mimeForName('a.odt')).toBe('application/vnd.oasis.opendocument.text')
    expect(mimeForName('a.ods')).toBe('application/vnd.oasis.opendocument.spreadsheet')
    expect(mimeForName('a.odp')).toBe('application/vnd.oasis.opendocument.presentation')
    expect(mimeForName('a.doc')).toBe('application/msword')
  })
})

