package cinc

import (
	"context"
	"errors"
	"fmt"
)

// DataBagItem is a single data bag item. It must contain an "id" key.
type DataBagItem map[string]any

// ID returns the item's "id" field.
func (i DataBagItem) ID() string {
	s, _ := i["id"].(string)
	return s
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

// Create adds a new item to the bag. The item must contain an "id".
func (s *DataBagItemsService) Create(ctx context.Context, item DataBagItem) (DataBagItem, *Response, error) {
	if item.ID() == "" {
		return nil, nil, errors.New("cinc: data bag item requires an \"id\"")
	}
	return s.write(ctx, "POST", s.coll(), item)
}

// Update replaces an existing item.
func (s *DataBagItemsService) Update(ctx context.Context, item DataBagItem) (DataBagItem, *Response, error) {
	if item.ID() == "" {
		return nil, nil, fmt.Errorf("cinc: data bag item requires an \"id\"")
	}
	return s.write(ctx, "PUT", s.item(item.ID()), item)
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
