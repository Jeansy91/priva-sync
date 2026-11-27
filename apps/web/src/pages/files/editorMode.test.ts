import { describe, it, expect } from 'vitest'
import { editorLanguage, isEditableText } from './editorMode'

describe('editorLanguage', () => {
  it('mappt Code-Endungen auf Monaco-Sprachen', () => {
    expect(editorLanguage('app.ts')).toBe('typescript')
    expect(editorLanguage('App.TSX')).toBe('typescript')
    expect(editorLanguage('main.js')).toBe('javascript')
    expect(editorLanguage('data.json')).toBe('json')
    expect(editorLanguage('README.md')).toBe('markdown')
    expect(editorLanguage('ci.yaml')).toBe('yaml')
    expect(editorLanguage('ci.yml')).toBe('yaml')
    expect(editorLanguage('run.sh')).toBe('shell')
    expect(editorLanguage('app.py')).toBe('python')
    expect(editorLanguage('main.go')).toBe('go')
    expect(editorLanguage('lib.rs')).toBe('rust')
    expect(editorLanguage('page.html')).toBe('html')
    expect(editorLanguage('style.css')).toBe('css')
    expect(editorLanguage('data.xml')).toBe('xml')
    expect(editorLanguage('q.sql')).toBe('sql')
    expect(editorLanguage('app.ini')).toBe('ini')
    expect(editorLanguage('nginx.conf')).toBe('ini')
  })

  it('mappt toml auf ini (kein eigenes Monaco-Grammar)', () => {
    expect(editorLanguage('Cargo.toml')).toBe('ini')
  })

  it('liefert plaintext fuer editierbaren Text ohne eigenes Grammar', () => {
    expect(editorLanguage('notes.txt')).toBe('plaintext')
    expect(editorLanguage('server.log')).toBe('plaintext')
    expect(editorLanguage('.env')).toBe('plaintext')
  })

  it('liefert null fuer nicht-editierbare Dateien', () => {
    expect(editorLanguage('photo.jpg')).toBeNull()
    expect(editorLanguage('archive.zip')).toBeNull()
    expect(editorLanguage('sheet.xlsx')).toBeNull()
    expect(editorLanguage('binary')).toBeNull()
  })
})

describe('isEditableText', () => {
  it('akzeptiert Code-, Konfig- und Plaintext-Endungen', () => {
    for (const n of ['a.ts', 'a.json', 'a.md', 'a.yml', 'a.sh', 'a.go', 'a.txt', 'a.log', 'a.conf']) {
      expect(isEditableText(n)).toBe(true)
    }
  })

  it('akzeptiert bekannte Dotfiles ohne Endung', () => {
    expect(isEditableText('.env')).toBe(true)
    expect(isEditableText('.gitignore')).toBe(true)
    expect(isEditableText('.editorconfig')).toBe(true)
  })

  it('lehnt Binaer-, Medien- und Office-Dateien ab', () => {
    for (const n of ['a.jpg', 'a.png', 'a.pdf', 'a.zip', 'a.mp4', 'a.xlsx', 'a.docx', 'a.exe']) {
      expect(isEditableText(n)).toBe(false)
    }
  })

  it('lehnt Dateien ohne Endung und unbekannte Namen ab', () => {
    expect(isEditableText('README')).toBe(false)
    expect(isEditableText('Makefile')).toBe(false)
    expect(isEditableText('')).toBe(false)
  })
})
