package smeldr

import "context"

// ContentParentProvider is implemented by content types that can host block
// sections and items (T94/A145). Register via [BlockHost] option on [App.Content],
// or manually via [App.RegisterBlockParent] for external providers.
type ContentParentProvider interface {
	// BlockParentTypeName returns the type identifier stored as parent_type in
	// the content-edge table (e.g. "post", "page").
	BlockParentTypeName() string

	// HasBlockParent reports whether the given ID exists in this content type's
	// repository. Returns false (no error) when the ID is not found.
	HasBlockParent(ctx context.Context, id string) (bool, error)
}

// blockHostProvider is an unexported marker interface combining
// [ContentParentProvider] with an internal flag. [Module[T]] implements it when
// the [BlockHost] option is given; [App.Content] checks it to auto-register.
type blockHostProvider interface {
	ContentParentProvider
	blockHostEnabled() bool
}
