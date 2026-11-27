import { describe, it, expect } from 'vitest'
import { getFileIconKind } from './fileIcon'

describe('getFileIconKind', () => {
  it('returns "text" for .txt', () => {
    expect(getFileIconKind('notes.txt')).toBe('text')
  })

  it.each([
    ['photo.jpg'], ['photo.jpeg'], ['pic.png'], ['ani.gif'],
    ['w.webp'], ['b.bmp'], ['v.svg'], ['icon.ico'],
    ['x.avif'], ['h.heic'], ['t.tiff'],
  ])('returns "image" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('image')
  })

  it('returns "pdf" for .pdf', () => {
    expect(getFileIconKind('doc.pdf')).toBe('pdf')
  })

  it.each([['a.doc'], ['a.docx'], ['a.odt']])('returns "word" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('word')
  })

  it.each([['a.rtf']])('returns "richtext" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('richtext')
  })

  it.each([['a.xls'], ['a.xlsx'], ['a.ods'], ['a.csv'], ['a.tsv']])(
    'returns "excel" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('excel')
    },
  )

  it.each([['a.ppt'], ['a.pptx'], ['a.odp'], ['a.key']])(
    'returns "ppt" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('ppt')
    },
  )

  it.each([['a.zip'], ['a.7z'], ['a.rar'], ['a.tar'], ['a.gz'], ['a.bz2'], ['a.xz'], ['a.zst']])(
    'returns "archive" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('archive')
    },
  )

  it.each([
    ['a.exe'], ['a.msi'], ['a.app'], ['a.AppImage'], ['a.deb'], ['a.rpm'],
  ])('returns "executable" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('executable')
  })

  it.each([
    ['a.sh'], ['a.cmd'], ['a.bat'], ['a.ps1'],
  ])('returns "shell" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('shell')
  })

  it.each([['a.mp3'], ['a.wav'], ['a.flac'], ['a.ogg'], ['a.m4a'], ['a.aac'], ['a.opus']])(
    'returns "audio" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('audio')
    },
  )

  it.each([['a.mp4'], ['a.mkv'], ['a.mov'], ['a.avi'], ['a.webm'], ['a.wmv'], ['a.flv']])(
    'returns "video" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('video')
    },
  )

  it.each([['a.js'], ['a.jsx'], ['a.mjs'], ['a.cjs']])('returns "js" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('js')
  })

  it.each([['a.ts'], ['a.tsx']])('returns "ts" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('ts')
  })

  it.each([['a.json'], ['a.jsonc']])('returns "json" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('json')
  })

  it.each([['a.html'], ['a.htm']])('returns "html" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('html')
  })

  it.each([['a.xml'], ['a.xsd']])('returns "xml" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('xml')
  })

  it.each([['a.yml'], ['a.yaml']])('returns "yaml" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('yaml')
  })

  it.each([['a.md'], ['a.markdown']])('returns "markdown" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('markdown')
  })

  it.each([['a.css'], ['a.scss'], ['a.sass'], ['a.less']])(
    'returns "css" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('css')
    },
  )

  it.each([
    ['a.py'], ['a.go'], ['a.rs'], ['a.java'], ['a.c'], ['a.cpp'], ['a.cc'],
    ['a.h'], ['a.hpp'], ['a.cs'], ['a.rb'], ['a.php'], ['a.swift'], ['a.kt'],
    ['a.lua'], ['a.r'], ['a.dart'],
  ])('returns "code" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('code')
  })

  it.each([['a.log']])('returns "text" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('text')
  })

  it.each([
    ['a.ini'], ['a.conf'], ['a.cfg'], ['a.toml'], ['a.env'], ['a.properties'],
  ])('returns "config" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('config')
  })

  it.each([['a.sql'], ['a.db'], ['a.sqlite'], ['a.sqlite3']])(
    'returns "database" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('database')
    },
  )

  it.each([['a.pem'], ['a.crt'], ['a.cer'], ['a.pfx'], ['a.p12']])(
    'returns "cert" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('cert')
    },
  )

  it.each([['a.ttf'], ['a.otf'], ['a.woff'], ['a.woff2'], ['a.eot']])(
    'returns "font" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('font')
    },
  )

  it.each([['a.epub'], ['a.mobi'], ['a.azw3']])('returns "ebook" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('ebook')
  })

  it.each([['a.eml'], ['a.msg'], ['a.mbox']])('returns "mail" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('mail')
  })

  it.each([['a.ics'], ['a.vcs']])('returns "calendar" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('calendar')
  })

  it.each([['a.vcf']])('returns "contact" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('contact')
  })

  it.each([['a.iso'], ['a.img'], ['a.vhd'], ['a.vmdk'], ['a.dmg']])(
    'returns "disk" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('disk')
    },
  )

  it.each([['a.patch'], ['a.diff']])('returns "diff" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('diff')
  })

  describe('special filenames (no extension)', () => {
    it.each([['Dockerfile'], ['dockerfile'], ['.dockerignore']])(
      'returns "config" for %s',
      (n) => {
        expect(getFileIconKind(n)).toBe('config')
      },
    )

    it.each([['Makefile'], ['makefile']])('returns "config" for %s', (n) => {
      expect(getFileIconKind(n)).toBe('config')
    })

    it.each([['.env'], ['.env.local'], ['.env.production']])(
      'returns "config" for %s',
      (n) => {
        expect(getFileIconKind(n)).toBe('config')
      },
    )

    it.each([['.gitignore'], ['.gitattributes']])('returns "config" for %s', (n) => {
      expect(getFileIconKind(n)).toBe('config')
    })

    it('returns "config" for README without ext as text', () => {
      expect(getFileIconKind('README')).toBe('text')
    })
  })

  // extended kinds

  it.each([
    ['m.stl'], ['m.amf'], ['m.3mf'], ['m.obj'], ['m.step'], ['m.stp'],
    ['m.gcode'], ['m.blend'], ['m.fbx'], ['m.dae'], ['m.glb'], ['m.gltf'],
    ['m.usdz'], ['m.ply'], ['m.x3d'], ['m.dwg'], ['m.dxf'], ['m.iges'],
    ['m.igs'], ['m.sldprt'], ['m.sldasm'],
  ])('returns "cad3d" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('cad3d')
  })

  it.each([
    ['a.psd'], ['a.ai'], ['a.xd'], ['a.fig'], ['a.sketch'],
    ['a.afdesign'], ['a.afphoto'], ['a.afpub'], ['a.eps'], ['a.cdr'],
    ['a.indd'], ['a.idml'], ['a.odg'],
  ])('returns "design" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('design')
  })

  it.each([
    ['a.dng'], ['a.raw'], ['a.cr2'], ['a.cr3'], ['a.nef'], ['a.arw'],
    ['a.raf'], ['a.orf'], ['a.rw2'],
  ])('returns "raw" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('raw')
  })

  it.each([
    ['a.geojson'], ['a.kml'], ['a.kmz'], ['a.gpx'], ['a.shp'],
    ['a.gpkg'], ['a.mbtiles'], ['a.osm'],
  ])('returns "gis" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('gis')
  })

  it.each([
    ['a.parquet'], ['a.arrow'], ['a.feather'], ['a.avro'], ['a.orc'],
    ['a.hdf5'], ['a.h5'], ['a.nc'], ['a.mat'], ['a.rds'], ['a.rdata'],
    ['a.npy'], ['a.npz'], ['a.pkl'], ['a.pickle'],
  ])('returns "data" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('data')
  })

  it.each([
    ['a.ipynb'], ['a.rmd'], ['a.qmd'], ['a.one'], ['a.onepkg'],
  ])('returns "notebook" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('notebook')
  })

  it.each([['a.bib'], ['a.ris'], ['a.enw']])('returns "bibliography" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('bibliography')
  })

  it.each([['a.tex'], ['a.sty'], ['a.cls']])('returns "latex" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('latex')
  })

  it.each([['a.srt'], ['a.vtt'], ['a.ass'], ['a.sub']])(
    'returns "subtitle" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('subtitle')
    },
  )

  it.each([['a.mid'], ['a.midi']])('returns "midi" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('midi')
  })

  it.each([['a.vsd'], ['a.vsdx'], ['a.drawio']])('returns "diagram" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('diagram')
  })

  it.each([['a.torrent']])('returns "torrent" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('torrent')
  })

  it.each([['a.bak'], ['a.old'], ['a.orig'], ['a.swp']])(
    'returns "backup" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('backup')
    },
  )

  it('returns "backup" for files ending with ~', () => {
    expect(getFileIconKind('config.txt~')).toBe('backup')
    expect(getFileIconKind('main.c~')).toBe('backup')
  })

  it.each([
    ['a.tmp'], ['a.temp'], ['a.crdownload'], ['a.part'], ['a.download'],
  ])('returns "tmp" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('tmp')
  })

  it.each([
    ['a.lock'], ['package-lock.json'], ['pnpm-lock.yaml'], ['yarn.lock'],
    ['Cargo.lock'], ['Gemfile.lock'], ['poetry.lock'], ['composer.lock'],
    ['go.sum'],
  ])('returns "lock" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('lock')
  })

  it.each([
    ['a.gpg'], ['a.asc'], ['a.sig'], ['a.sigstore'], ['a.sha256'], ['a.md5'],
  ])('returns "signature" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('signature')
  })

  // extended mappings for existing kinds

  it.each([['a.pages'], ['a.dot'], ['a.dotx'], ['a.ott']])(
    'returns "word" for apple/template %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('word')
    },
  )

  it.each([['a.numbers'], ['a.xlt'], ['a.xltx'], ['a.ots'], ['a.odc'], ['a.odf']])(
    'returns "excel" for apple/template %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('excel')
    },
  )

  it.each([['a.keynote'], ['a.pot'], ['a.potx'], ['a.otp']])(
    'returns "ppt" for apple/template %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('ppt')
    },
  )

  it.each([['a.xps'], ['a.oxps'], ['a.djvu'], ['a.ps']])(
    'returns "pdf" for document-like %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('pdf')
    },
  )

  it.each([['a.aif'], ['a.aiff'], ['a.wma'], ['a.alac'], ['a.als'], ['a.flp']])(
    'returns "audio" for %s',
    (n) => {
      expect(getFileIconKind(n)).toBe('audio')
    },
  )

  it.each([
    ['a.mxf'], ['a.m2ts'], ['a.vob'], ['a.ts.mp4'], ['a.m3u8'],
    ['a.prproj'], ['a.aep'],
  ])('returns "video" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('video')
  })

  it.each([
    ['a.apk'], ['a.ipa'], ['a.aab'], ['a.xcarchive'], ['a.wasm'],
  ])('returns "executable" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('executable')
  })

  it.each([['a.kdbx'], ['a.pub'], ['a.1pux']])('returns "cert" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('cert')
  })

  it.each([
    ['a.tf'], ['a.tfvars'], ['a.tfstate'], ['a.hcl'], ['a.nomad'],
    ['a.nix'], ['a.reg'], ['a.plist'], ['a.desktop'],
  ])('returns "config" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('config')
  })

  it.each([
    ['a.vue'], ['a.svelte'], ['a.astro'], ['a.mdx'],
    ['a.pug'], ['a.ejs'], ['a.hbs'], ['a.handlebars'], ['a.liquid'], ['a.njk'],
    ['a.graphql'], ['a.gql'], ['a.proto'], ['a.thrift'],
    ['a.glsl'], ['a.hlsl'], ['a.wgsl'], ['a.frag'], ['a.vert'], ['a.comp'],
    ['a.clj'], ['a.cljs'], ['a.ex'], ['a.exs'], ['a.erl'], ['a.hrl'],
    ['a.hs'], ['a.ml'], ['a.mli'], ['a.fs'], ['a.fsx'], ['a.nim'],
    ['a.zig'], ['a.v'], ['a.cr'], ['a.jl'], ['a.pl'], ['a.pm'],
    ['a.groovy'], ['a.gradle'], ['a.scala'], ['a.sbt'], ['a.asm'], ['a.s'],
    ['a.vb'], ['a.vbs'], ['a.pas'], ['a.f90'], ['a.f95'], ['a.cob'],
    ['a.ada'], ['a.tcl'], ['a.lisp'], ['a.scm'], ['a.rkt'],
    ['a.map'], ['a.coffee'],
  ])('returns "code" for %s', (n) => {
    expect(getFileIconKind(n)).toBe('code')
  })

  it.each([
    ['Containerfile'], ['Jenkinsfile'], ['Vagrantfile'],
    ['Gemfile'], ['Procfile'], ['Brewfile'], ['Justfile'],
  ])('returns "config" for special filename %s', (n) => {
    expect(getFileIconKind(n)).toBe('config')
  })

  it('returns "code" for Rakefile', () => {
    expect(getFileIconKind('Rakefile')).toBe('code')
  })

  describe('robustness', () => {
    it('is case-insensitive', () => {
      expect(getFileIconKind('PIC.PNG')).toBe('image')
      expect(getFileIconKind('Doc.PdF')).toBe('pdf')
    })

    it('handles compound .tar.* as archive', () => {
      expect(getFileIconKind('a.tar.gz')).toBe('archive')
      expect(getFileIconKind('a.tar.bz2')).toBe('archive')
      expect(getFileIconKind('a.tar.xz')).toBe('archive')
      expect(getFileIconKind('a.tar.zst')).toBe('archive')
    })

    it('falls back to "unknown" for unknown extension', () => {
      expect(getFileIconKind('mystery.xyz123')).toBe('unknown')
    })

    it('falls back to "unknown" when no extension', () => {
      expect(getFileIconKind('weirdfile')).toBe('unknown')
    })
  })
})
