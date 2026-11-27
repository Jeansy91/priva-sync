use std::io;
use std::path::Path;

use windows::core::{HSTRING, PCWSTR};
use windows::Win32::Foundation::CloseHandle;
use windows::Win32::Storage::FileSystem::{
    CreateFileW, GetFileInformationByHandleEx, FileIdInfo, FILE_FLAG_BACKUP_SEMANTICS,
    FILE_ID_INFO, FILE_READ_ATTRIBUTES, FILE_SHARE_DELETE, FILE_SHARE_READ, FILE_SHARE_WRITE,
    OPEN_EXISTING,
};

/// Reads `FILE_ID_INFO` for `path`: a 64-bit volume serial plus a 128-bit
/// file id. Unlike the legacy `nFileIndexHigh/Low` pair, this doesn't
/// truncate ReFS's wider ids into a collision-prone 64 bits (current MS
/// guidance — see ADR-0063 for the comparison).
///
/// `FILE_FLAG_BACKUP_SEMANTICS` is required to open a directory handle at
/// all, so this works for both files and directories, mirroring the
/// existing `cloud_files::windows::with_handle` pattern.
pub(super) fn read(path: &Path) -> io::Result<Vec<u8>> {
    let path_str = path.to_str().ok_or_else(|| {
        io::Error::new(io::ErrorKind::InvalidInput, "non-UTF-8 path")
    })?;
    let path_w = HSTRING::from(path_str);

    let handle = unsafe {
        CreateFileW(
            PCWSTR(path_w.as_ptr()),
            FILE_READ_ATTRIBUTES.0,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            None,
            OPEN_EXISTING,
            FILE_FLAG_BACKUP_SEMANTICS,
            None,
        )
    }
    .map_err(win_err_to_io)?;

    let mut info = FILE_ID_INFO::default();
    let result = unsafe {
        GetFileInformationByHandleEx(
            handle,
            FileIdInfo,
            &mut info as *mut _ as *mut std::ffi::c_void,
            std::mem::size_of::<FILE_ID_INFO>() as u32,
        )
    };
    unsafe {
        let _ = CloseHandle(handle);
    }
    result.map_err(win_err_to_io)?;

    let mut bytes = Vec::with_capacity(24);
    bytes.extend_from_slice(&info.VolumeSerialNumber.to_le_bytes());
    bytes.extend_from_slice(&info.FileId.Identifier);
    Ok(bytes)
}

fn win_err_to_io(e: windows::core::Error) -> io::Error {
    io::Error::other(e)
}
