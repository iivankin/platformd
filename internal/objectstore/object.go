package objectstore

import "io"

type PutInput struct {
	StoreID        string
	ObjectKey      string
	ContentType    string
	ExpectedSHA256 string
	Body           io.Reader
	BodySize       int64
	BodySizeKnown  bool
	Precondition   ObjectPrecondition
	PreserveETag   string
	ModTimeMillis  int64
}

type Object struct {
	Metadata ObjectMetadata
}

type ObjectMetadata struct {
	ObjectStoreID   string
	ObjectKey       string
	ContentType     string
	ETag            string
	Size            int64
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type ObjectPrecondition struct {
	IfMatch     string
	IfNoneMatch bool
}

type ObjectListEntry struct {
	Object       *ObjectMetadata
	CommonPrefix string
}
