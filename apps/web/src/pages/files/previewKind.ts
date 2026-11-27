export type PreviewKind = 'image' | 'pdf' | 'video' | 'audio' | 'model3d' | 'font' | 'epub' | 'archive' | 'comic' | 'office'

type Entry = { kind: PreviewKind; mime: string }

const EXT: Record<string, Entry> = {
  jpg:  { kind: 'image', mime: 'image/jpeg'    },
  jpeg: { kind: 'image', mime: 'image/jpeg'    },
  png:  { kind: 'image', mime: 'image/png'     },
  gif:  { kind: 'image', mime: 'image/gif'     },
  webp: { kind: 'image', mime: 'image/webp'    },
  svg:  { kind: 'image', mime: 'image/svg+xml' },
  avif: { kind: 'image', mime: 'image/avif'    },
  bmp:  { kind: 'image', mime: 'image/bmp'     },

  pdf:  { kind: 'pdf',   mime: 'application/pdf' },

  mp4:  { kind: 'video', mime: 'video/mp4'      },
  webm: { kind: 'video', mime: 'video/webm'     },
  ogv:  { kind: 'video', mime: 'video/ogg'      },
  mov:  { kind: 'video', mime: 'video/quicktime'},
  m4v:  { kind: 'video', mime: 'video/mp4'      },

  mp3:  { kind: 'audio', mime: 'audio/mpeg' },
  wav:  { kind: 'audio', mime: 'audio/wav'  },
  ogg:  { kind: 'audio', mime: 'audio/ogg'  },
  flac: { kind: 'audio', mime: 'audio/flac' },
  m4a:  { kind: 'audio', mime: 'audio/mp4'  },
  aac:  { kind: 'audio', mime: 'audio/aac'  },

  stl:  { kind: 'model3d', mime: 'model/stl'             },
  '3mf':{ kind: 'model3d', mime: 'model/3mf'             },
  obj:  { kind: 'model3d', mime: 'model/obj'             },
  gltf: { kind: 'model3d', mime: 'model/gltf+json'       },
  glb:  { kind: 'model3d', mime: 'model/gltf-binary'     },
  ply:  { kind: 'model3d', mime: 'application/octet-stream' },
  fbx:  { kind: 'model3d', mime: 'application/octet-stream' },
  dae:  { kind: 'model3d', mime: 'model/vnd.collada+xml' },
  usdz: { kind: 'model3d', mime: 'model/vnd.usdz+zip'    },

  // Bilder, die im Browser keinen nativen Decoder haben — werden per JS dekodiert.
  heic: { kind: 'image', mime: 'image/heic'  },
  heif: { kind: 'image', mime: 'image/heif'  },
  tif:  { kind: 'image', mime: 'image/tiff'  },
  tiff: { kind: 'image', mime: 'image/tiff'  },

  ttf:  { kind: 'font', mime: 'font/ttf'    },
  otf:  { kind: 'font', mime: 'font/otf'    },
  woff: { kind: 'font', mime: 'font/woff'   },
  woff2:{ kind: 'font', mime: 'font/woff2'  },

  epub: { kind: 'epub', mime: 'application/epub+zip' },

  zip:  { kind: 'archive', mime: 'application/zip' },

  cbz:  { kind: 'comic', mime: 'application/vnd.comicbook+zip' },
  cbr:  { kind: 'comic', mime: 'application/vnd.comicbook-rar' },

  // Office preview/edit via an externally configured document server
  // (ADR-0027, format range per ADR-0049). Mirrors officedoc.extTypes on
  // the server — keep the two in sync. Excluded on purpose: PDF (own
  // viewer above), and the text/markup formats the document server also
  // accepts but this app opens in its own editor (txt, csv, tsv, md, xml,
  // html) or dedicated viewers (epub).
  docx: { kind: 'office', mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' },
  dotx: { kind: 'office', mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.template' },
  docm: { kind: 'office', mime: 'application/vnd.ms-word.document.macroEnabled.12' },
  dotm: { kind: 'office', mime: 'application/vnd.ms-word.template.macroEnabled.12' },
  doc:  { kind: 'office', mime: 'application/msword' },
  dot:  { kind: 'office', mime: 'application/msword' },
  odt:  { kind: 'office', mime: 'application/vnd.oasis.opendocument.text' },
  ott:  { kind: 'office', mime: 'application/vnd.oasis.opendocument.text-template' },
  fodt: { kind: 'office', mime: 'application/vnd.oasis.opendocument.text-flat-xml' },
  rtf:  { kind: 'office', mime: 'application/rtf' },
  sxw:  { kind: 'office', mime: 'application/vnd.sun.xml.writer' },
  stw:  { kind: 'office', mime: 'application/vnd.sun.xml.writer.template' },
  wps:  { kind: 'office', mime: 'application/vnd.ms-works' },
  wpt:  { kind: 'office', mime: 'application/vnd.ms-works' },
  pages:{ kind: 'office', mime: 'application/vnd.apple.pages' },

  xlsx: { kind: 'office', mime: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' },
  xltx: { kind: 'office', mime: 'application/vnd.openxmlformats-officedocument.spreadsheetml.template' },
  xlsm: { kind: 'office', mime: 'application/vnd.ms-excel.sheet.macroEnabled.12' },
  xltm: { kind: 'office', mime: 'application/vnd.ms-excel.template.macroEnabled.12' },
  xlsb: { kind: 'office', mime: 'application/vnd.ms-excel.sheet.binary.macroEnabled.12' },
  xls:  { kind: 'office', mime: 'application/vnd.ms-excel' },
  xlt:  { kind: 'office', mime: 'application/vnd.ms-excel' },
  ods:  { kind: 'office', mime: 'application/vnd.oasis.opendocument.spreadsheet' },
  ots:  { kind: 'office', mime: 'application/vnd.oasis.opendocument.spreadsheet-template' },
  fods: { kind: 'office', mime: 'application/vnd.oasis.opendocument.spreadsheet-flat-xml' },
  sxc:  { kind: 'office', mime: 'application/vnd.sun.xml.calc' },
  et:   { kind: 'office', mime: 'application/vnd.ms-excel' },
  ett:  { kind: 'office', mime: 'application/vnd.ms-excel' },
  numbers: { kind: 'office', mime: 'application/vnd.apple.numbers' },

  pptx: { kind: 'office', mime: 'application/vnd.openxmlformats-officedocument.presentationml.presentation' },
  potx: { kind: 'office', mime: 'application/vnd.openxmlformats-officedocument.presentationml.template' },
  ppsx: { kind: 'office', mime: 'application/vnd.openxmlformats-officedocument.presentationml.slideshow' },
  pptm: { kind: 'office', mime: 'application/vnd.ms-powerpoint.presentation.macroEnabled.12' },
  potm: { kind: 'office', mime: 'application/vnd.ms-powerpoint.template.macroEnabled.12' },
  ppsm: { kind: 'office', mime: 'application/vnd.ms-powerpoint.slideshow.macroEnabled.12' },
  ppt:  { kind: 'office', mime: 'application/vnd.ms-powerpoint' },
  pot:  { kind: 'office', mime: 'application/vnd.ms-powerpoint' },
  pps:  { kind: 'office', mime: 'application/vnd.ms-powerpoint' },
  odp:  { kind: 'office', mime: 'application/vnd.oasis.opendocument.presentation' },
  otp:  { kind: 'office', mime: 'application/vnd.oasis.opendocument.presentation-template' },
  fodp: { kind: 'office', mime: 'application/vnd.oasis.opendocument.presentation-flat-xml' },
  odg:  { kind: 'office', mime: 'application/vnd.oasis.opendocument.graphics' },
  sxi:  { kind: 'office', mime: 'application/vnd.sun.xml.impress' },
  dps:  { kind: 'office', mime: 'application/vnd.ms-powerpoint' },
  dpt:  { kind: 'office', mime: 'application/vnd.ms-powerpoint' },
  key:  { kind: 'office', mime: 'application/vnd.apple.keynote' },
}

function entryFor(name: string): Entry | null {
  const dot = name.lastIndexOf('.')
  if (dot < 0 || dot === name.length - 1) return null
  return EXT[name.slice(dot + 1).toLowerCase()] ?? null
}

// Extensions we cannot preview but still want to label with a sensible
// MIME so the detail panel shows "Shell-Skript" or "Disk Image" instead
// of "unknown". Keep this list cheap to extend — anything that
// the OS would handle via Office/LibreOffice / standard editors fits.
const EXTRA_MIME: Record<string, string> = {
  txt:  'text/plain',
  md:   'text/markdown',
  csv:  'text/csv',
  tsv:  'text/tab-separated-values',
  json: 'application/json',
  xml:  'application/xml',
  yaml: 'application/yaml',
  yml:  'application/yaml',
  html: 'text/html',
  htm:  'text/html',
  css:  'text/css',
  js:   'text/javascript',
  ts:   'text/typescript',
  log:  'text/plain',
  ini:  'text/plain',
  conf: 'text/plain',
  sh:   'application/x-sh',
  rar:  'application/vnd.rar',
  '7z': 'application/x-7z-compressed',
  tar:  'application/x-tar',
  gz:   'application/gzip',
  bz2:  'application/x-bzip2',
  exe:  'application/vnd.microsoft.portable-executable',
  msi:  'application/x-msdownload',
  dmg:  'application/x-apple-diskimage',
  iso:  'application/x-iso9660-image',
  apk:  'application/vnd.android.package-archive',
  deb:  'application/vnd.debian.binary-package',
  rpm:  'application/x-rpm',
}

export function previewKindFor(name: string): PreviewKind | null {
  return entryFor(name)?.kind ?? null
}

export function mimeForName(name: string): string | null {
  const previewMime = entryFor(name)?.mime
  if (previewMime) return previewMime
  const dot = name.lastIndexOf('.')
  if (dot < 0 || dot === name.length - 1) return null
  return EXTRA_MIME[name.slice(dot + 1).toLowerCase()] ?? null
}
