package scan

import (
	"encoding/binary"
	"io/fs"
	"syscall"
	"unsafe"
)

// The getattrlist(2) constants that entrySize uses, from <sys/attr.h>.
const (
	attrBitMapCount = 5

	attrCmnDevID         = 0x00000002
	attrCmnObjType       = 0x00000008
	attrCmnFlags         = 0x00040000
	attrCmnFileID        = 0x02000000
	attrCmnReturnedAttrs = 0x80000000

	attrFileLinkCount = 0x00000001
	attrFileAllocSize = 0x00000004

	attrCmnExtPrivateSize = 0x00000008
	attrCmnExtCloneID     = 0x00000100
	attrCmnExtCloneRefcnt = 0x00001000

	fsoptNoFollow         = 0x00000001
	fsoptPackInvalAttrs   = 0x00000008
	fsoptAttrCmnExtended  = 0x00000020
	vreg                  = 1    // fsobj_type_t of a regular file
	ufCompressed          = 0x20 // UF_COMPRESSED in st_flags
	attrBufSize           = 128
	requiredCommonAttrs   = attrCmnDevID | attrCmnObjType | attrCmnFileID
	requiredFileAttrs     = attrFileLinkCount | attrFileAllocSize
	requestedCommonAttrs  = attrCmnReturnedAttrs | requiredCommonAttrs | attrCmnFlags
	requestedForkAttrs    = attrCmnExtPrivateSize | attrCmnExtCloneID | attrCmnExtCloneRefcnt
	attrListOptions       = fsoptNoFollow | fsoptPackInvalAttrs | fsoptAttrCmnExtended
	returnedAttrsOffset   = 4  // after the u_int32_t length
	attrValuesOffset      = 24 // after the length and the five returned bitmaps
	privateSizeIsReturned = attrCmnExtPrivateSize
)

// attrList is struct attrlist.
type attrList struct {
	bitmapCount uint16
	reserved    uint16
	commonAttr  uint32
	volAttr     uint32
	dirAttr     uint32
	fileAttr    uint32
	forkAttr    uint32
}

// entrySize measures a regular file with getattrlist(2), which reports
// the bytes that only the file uses (ATTR_CMNEXT_PRIVATESIZE) on APFS.
// A file that is a clone, or that has clones, shares blocks with them,
// and deleting it frees only its private bytes while a clone remains.
// It also reports the file's clone family and the number of files in it,
// so tally can count the shared blocks when the walk finds every clone.
//
// It uses the allocated size, as lstat gives it, for a compressed file,
// whose data APFS does not count in the private size,
// and on a volume that does not report the private size.
// It uses lstat for an entry that is not a regular file,
// and when getattrlist fails.
func entrySize(path string, d fs.DirEntry) (fileSize, bool) {
	if !d.Type().IsRegular() {
		return statSize(d)
	}
	f, ok := attrSize(path)
	if !ok {
		return statSize(d)
	}
	return f, true
}

func attrSize(path string) (fileSize, bool) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return fileSize{}, false
	}
	al := attrList{
		bitmapCount: attrBitMapCount,
		commonAttr:  requestedCommonAttrs,
		fileAttr:    requiredFileAttrs,
		forkAttr:    requestedForkAttrs,
	}
	var buf [attrBufSize]byte
	_, _, errno := syscall.Syscall6(syscall.SYS_GETATTRLIST,
		uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&al)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), attrListOptions, 0)
	if errno != 0 {
		return fileSize{}, false
	}
	return parseAttrs(buf[:])
}

// parseAttrs reads the attributes that attrSize requests.
// With FSOPT_PACK_INVAL_ATTRS, every requested attribute has a place
// in the buffer, in the order of its bit, and the returned bitmaps say
// which ones the volume filled in. Each value is aligned to 4 bytes.
func parseAttrs(buf []byte) (fileSize, bool) {
	le := binary.LittleEndian
	if int(le.Uint32(buf)) > len(buf) {
		return fileSize{}, false
	}
	ret := buf[returnedAttrsOffset:attrValuesOffset]
	common, file, fork := le.Uint32(ret[0:]), le.Uint32(ret[12:]), le.Uint32(ret[16:])
	if common&requiredCommonAttrs != requiredCommonAttrs || file&requiredFileAttrs != requiredFileAttrs {
		return fileSize{}, false
	}
	v := buf[attrValuesOffset:]
	dev := int32(le.Uint32(v[0:]))
	objType := le.Uint32(v[4:])
	flags := le.Uint32(v[8:])
	fileID := le.Uint64(v[12:])
	links := le.Uint32(v[20:])
	alloc := int64(le.Uint64(v[24:]))
	private := int64(le.Uint64(v[32:]))
	cloneID := le.Uint64(v[40:])
	clones := le.Uint32(v[48:])
	if objType != vreg || alloc < 0 {
		return fileSize{}, false
	}
	if common&attrCmnFlags == 0 {
		flags = 0
	}
	// The same device number as lstat's, so hard links match either way.
	f := fileSize{
		bytes:  alloc,
		id:     inode{dev: uint64(dev), ino: fileID},
		linked: links > 1,
	}
	if fork&privateSizeIsReturned == 0 || flags&ufCompressed != 0 || private < 0 || private > alloc {
		return f, true
	}
	f.bytes = private
	if fork&attrCmnExtCloneID != 0 && fork&attrCmnExtCloneRefcnt != 0 && clones > 1 {
		f.family = cloneFamily{dev: uint64(dev), id: cloneID}
		f.clones = clones
		f.shared = alloc - private
	}
	return f, true
}
