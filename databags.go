package cinc

import (
	"context"
	"errors"
)

// DataBagItem is a single data bag item. It must contain an "id" key.
type DataBagItem map[string]any

// ID returns the item's "id" field.
func (i DataBagItem) ID() string {
	s, _ := i["id"].(string)
	return s
}

// ErrMissingDataBagItemID means a data bag item has no "id", or its "id" is
// not a non-empty string. A Chef Server stores and indexes items by it.
var ErrMissingDataBagItemID = errors.New("cinc: data bag item requires a non-empty string \"id\"")

// Validate reports whether the item can be written to a Chef Server: it
// returns ErrMissingDataBagItemID unless the item has a non-empty string
// "id". Create, Update and Encrypt run the same check; call Validate to vet
// an item (say, one a user just edited) before doing anything else with it.
func (i DataBagItem) Validate() error {
	if i.ID() == "" {
		return ErrMissingDataBagItemID
	}
	return nil
}

// Content returns a copy of the item's own values: every key except "id"
// and the plaintext "chef_type"/"data_bag" keys a Chef Server adds to the
// items it echoes back or returns from search. An encrypted value under one
// of those two names is kept, as Decrypt keeps it. Use it to show or compare
// what an item holds without its bookkeeping.
func (i DataBagItem) Content() map[string]any {
	out := make(map[string]any, len(i))
	for k, v := range i {
		if k == "id" || isServerItemValue(k, v) {
			continue
		}
		out[k] = v
	}
	return out
}

// DataBagsService accesses the /data endpoints.
type DataBagsService struct{ client *Client }

// List returns the data bag name->URL index.
func (s *DataBagsService) List(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET", s.client.orgPath("/data"), nil)
}

// Create creates an empty data bag.
func (s *DataBagsService) Create(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "POST",
		s.client.orgPath("/data"), map[string]string{"name": name})
	return resp, err
}

// Delete removes a data bag and all its items.
func (s *DataBagsService) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/data/"+esc(name)), nil)
	return resp, err
}

// Items returns a handle to the items of one data bag.
func (s *DataBagsService) Items(bag string) *DataBagItemsService {
	return &DataBagItemsService{client: s.client, bag: bag}
}

// DataBagItemsService accesses the items within a single data bag.
type DataBagItemsService struct {
	client *Client
	bag    string
}

func (s *DataBagItemsService) coll() string { return s.client.orgPath("/data/" + esc(s.bag)) }
func (s *DataBagItemsService) item(id string) string {
	return s.client.orgPath("/data/" + esc(s.bag) + "/" + esc(id))
}

// List returns the item id->URL index for the bag.
func (s *DataBagItemsService) List(ctx context.Context) (map[string]string, *Response, error) {
	return do[map[string]string](ctx, s.client, "GET", s.coll(), nil)
}

// Get retrieves a data bag item by id.
func (s *DataBagItemsService) Get(ctx context.Context, id string) (DataBagItem, *Response, error) {
	return do[DataBagItem](ctx, s.client, "GET", s.item(id), nil)
}

// Create adds a new item to the bag. It returns an ErrMissingDataBagItemID
// error, without sending anything, if the item fails Validate.
func (s *DataBagItemsService) Create(ctx context.Context, item DataBagItem) (DataBagItem, *Response, error) {
	if err := item.Validate(); err != nil {
		return nil, nil, err
	}
	return s.write(ctx, "POST", s.coll(), item)
}

// Update replaces an existing item. It returns an ErrMissingDataBagItemID
// error, without sending anything, if the item fails Validate.
func (s *DataBagItemsService) Update(ctx context.Context, item DataBagItem) (DataBagItem, *Response, error) {
	if err := item.Validate(); err != nil {
		return nil, nil, err
	}
	return s.write(ctx, "PUT", s.item(item.ID()), item)
}

// GetDecrypted retrieves an encrypted item and decrypts it with secret (see
// DataBagItem.Decrypt). A plaintext item yields an ErrNotEncrypted error and
// a wrong secret an ErrDataBagAuth error; the item is nil in both cases, and
// the Response is the GET's.
func (s *DataBagItemsService) GetDecrypted(ctx context.Context, id string, secret []byte) (DataBagItem, *Response, error) {
	item, resp, err := s.Get(ctx, id)
	if err != nil {
		return nil, resp, err
	}
	plain, err := item.Decrypt(secret)
	if err != nil {
		return nil, resp, err
	}
	return plain, resp, nil
}

// CreateEncrypted encrypts a plaintext item with secret (see
// DataBagItem.Encrypt) and adds it to the bag. The item is not modified.
// Nothing is sent if the item fails Validate or is already encrypted
// (ErrAlreadyEncrypted). The returned item is the server's echo, which is the
// encrypted item as stored.
func (s *DataBagItemsService) CreateEncrypted(ctx context.Context, item DataBagItem, secret []byte) (DataBagItem, *Response, error) {
	enc, err := item.Encrypt(secret)
	if err != nil {
		return nil, nil, err
	}
	return s.Create(ctx, enc)
}

// UpdateEncrypted encrypts a plaintext item with secret and replaces the
// stored item with it, like CreateEncrypted does for a new one. An edit is
// GetDecrypted, a change to the plaintext, then UpdateEncrypted.
func (s *DataBagItemsService) UpdateEncrypted(ctx context.Context, item DataBagItem, secret []byte) (DataBagItem, *Response, error) {
	enc, err := item.Encrypt(secret)
	if err != nil {
		return nil, nil, err
	}
	return s.Update(ctx, enc)
}

// serverItemKeys are the keys a Chef Server adds to the item it echoes back
// from a POST or PUT ("chef_type":"data_bag_item", "data_bag":"<bag>"). They
// are not part of the stored item, which a GET returns without them.
var serverItemKeys = [...]string{"chef_type", "data_bag"}

// write sends item and returns the server's echo of it with the
// server-added keys put back as they were sent: dropped if the caller's item
// lacked them, restored if it had them (the server stores the caller's
// value but overwrites it in the response). Returning the added keys would
// make an encrypted item undecryptable and, if PUT back unwrapped, get them
// stored for good.
func (s *DataBagItemsService) write(ctx context.Context, method, path string, item DataBagItem) (DataBagItem, *Response, error) {
	out, resp, err := do[DataBagItem](ctx, s.client, method, path, item)
	if out != nil {
		for _, k := range serverItemKeys {
			if v, ok := item[k]; ok {
				out[k] = v
			} else {
				delete(out, k)
			}
		}
	}
	return out, resp, err
}

// Delete removes an item by id.
func (s *DataBagItemsService) Delete(ctx context.Context, id string) (*Response, error) {
	_, resp, err := do[DataBagItem](ctx, s.client, "DELETE", s.item(id), nil)
	return resp, err
}
