// order-updated is a CGI script called by a Stripe webhook whenever an order is
// updated.  It updates the Orders spreadsheet in Google Docs to reflect the
// changes.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/scholacantorum/public-site-backend/backend-log"
	"github.com/scholacantorum/public-site-backend/private"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/customer"
	"github.com/stripe/stripe-go/sku"
	"github.com/stripe/stripe-go/webhook"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/sheets/v4"
)

var webhookSecret string
var sheet string

func main() {
	belog.LogApp = "order-updated"
	defer func() {
		if err := recover(); err != nil {
			belog.Log("PANIC: %s", err)
			panic(err)
		}
	}()
	http.Handle("/backend/order-updated", http.HandlerFunc(handler))
	cgi.Serve(nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	var lockfh *os.File
	var body bytes.Buffer
	var ev stripe.Event
	var order stripe.Order
	var conf *jwt.Config
	var client *http.Client
	var svc *sheets.Service
	var ss *sheets.Spreadsheet
	var sheetnum int64
	var vr *sheets.BatchGetValuesResponse
	var requests []*sheets.Request
	var items = map[string]*stripe.OrderItem{}
	var onum int
	var err error

	// Determine parameters for mode.
	switch cwd, _ := os.Getwd(); cwd {
	case "/home/scholacantorum/scholacantorum.org/backend", "/home/scholacantorum/scholacantorum.org/public/backend":
		webhookSecret = private.StripeLiveOrderUpdatedWebhookSecret
		sheet = private.OrderSheetLive
		stripe.Key = private.StripeLiveSecretKey
		belog.LogMode = "LIVE"
	case "/home/scholacantorum/new.scholacantorum.org/backend":
		webhookSecret = private.StripeTestOrderUpdatedWebhookSecret
		sheet = private.OrderSheetTest
		stripe.Key = private.StripeTestSecretKey
		belog.LogMode = "TEST"
	default:
		err = fmt.Errorf("backend/order-updated run from unrecognized directory")
		goto ERROR
	}

	// Obtain a file lock to ensure that we don't have parallel spreadsheet
	// updates.
	if lockfh, err = os.OpenFile("/home/scholacantorum/order-updated-lock", os.O_CREATE|os.O_RDWR, 0644); err != nil {
		goto ERROR
	}
	defer lockfh.Close()
	if err = syscall.Flock(int(lockfh.Fd()), syscall.LOCK_EX); err != nil {
		goto ERROR
	}

	// Verify the request and get the order data from it.
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, err = io.Copy(&body, r.Body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if ev, err = webhook.ConstructEvent(body.Bytes(), r.Header.Get("Stripe-Signature"), webhookSecret); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if ev.Type != "order.updated" {
		return
	}
	if err = json.Unmarshal(ev.Data.Raw, &order); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if onum, err = strconv.Atoi(order.Metadata["order-number"]); err != nil {
		err = fmt.Errorf("can't get order-number from order: %s", err)
		goto ERROR
	}

	// Establish a connection to Google Sheets, as admin@scholacantorum.org.
	conf = &jwt.Config{
		Email:      private.SheetsClientEmail,
		PrivateKey: []byte(private.SheetsPrivateKey),
		Scopes: []string{
			"https://www.googleapis.com/auth/spreadsheets",
		},
		TokenURL: google.JWTTokenURL,
		Subject:  "admin@scholacantorum.org",
	}
	client = conf.Client(oauth2.NoContext)
	if svc, err = sheets.New(client); err != nil {
		goto ERROR
	}

	// Get the sheet number for the "Orders" sheet.
	if ss, err = svc.Spreadsheets.Get(sheet).Fields(googleapi.Field("sheets.properties")).Do(); err != nil {
		goto ERROR
	}
	for _, sh := range ss.Sheets {
		if sh.Properties.Title == "Orders" {
			sheetnum = sh.Properties.SheetId
			break
		}
	}
	if sheetnum == 0 {
		err = fmt.Errorf(`no "Orders" sheet found in spreadsheet`)
		goto ERROR
	}

	// Get the order IDs and SKUs from columns D and L.
	if vr, err = svc.Spreadsheets.Values.BatchGet(sheet).Ranges("Orders!D:D", "Orders!L:L").Do(); err != nil {
		goto ERROR
	}

	// Build the list of SKU lines for this order, net of returns.
	for _, i := range order.Items {
		if i.Type == "sku" {
			li := items[i.Parent]
			if li == nil {
				items[i.Parent] = i
			} else {
				li.Amount += i.Amount
				li.Quantity += i.Quantity
			}
		}
	}
	for _, r := range order.Returns.Data {
		for _, i := range r.Items {
			if i.Type == "sku" {
				li := items[i.Parent]
				if li == nil { // shouldn't be possible
					items[i.Parent] = i
					i.Amount *= -1
					i.Quantity *= -1
				} else {
					li.Amount -= i.Amount
					li.Quantity -= i.Quantity
				}
			}
		}
	}
	for s, i := range items {
		if i.Quantity == 0 && i.Amount == 0 {
			delete(items, s)
		}
	}

	// Walk the list of rows in the spreadsheet, looking for ones that
	// belong to this order.  We walk from the bottom up so that deletions
	// don't change row numbers we care about.
	for row := len(vr.ValueRanges[0].Values) - 1; row >= 0; row-- {
		var rowoid string
		var sku string
		var item *stripe.OrderItem
		var ok bool

		if len(vr.ValueRanges[0].Values[row]) < 1 {
			continue
		}
		rowoid, ok = vr.ValueRanges[0].Values[row][0].(string)
		if !ok || rowoid != order.ID {
			continue
		}
		if len(vr.ValueRanges[1].Values[row]) < 1 {
			continue
		}
		sku, ok = vr.ValueRanges[1].Values[row][0].(string)
		if ok {
			item, ok = items[sku]
		}
		if !ok {
			// This row is for our order, with an unknown SKU —
			// probably something that was returned.  We want to
			// delete the row.
			requests = append(requests, &sheets.Request{DeleteDimension: &sheets.DeleteDimensionRequest{
				Range: &sheets.DimensionRange{
					Dimension:  "ROWS",
					StartIndex: int64(row),
					EndIndex:   int64(row) + 1,
					SheetId:    sheetnum,
				},
			}})
			continue
		}
		// We found a row for this SKU.  Update the quantity and total
		// on it.  We'll leave the unit price unchanged.
		if sku != "donation" {
			requests = append(requests, &sheets.Request{UpdateCells: &sheets.UpdateCellsRequest{
				Start: &sheets.GridCoordinate{
					SheetId:     sheetnum,
					RowIndex:    int64(row),
					ColumnIndex: 12, // M, zero based
				},
				Fields: "userEnteredValue",
				Rows: []*sheets.RowData{{Values: []*sheets.CellData{{
					UserEnteredValue: &sheets.ExtendedValue{
						NumberValue: float64(item.Quantity),
					},
				}}}},
			}})
		}
		requests = append(requests, &sheets.Request{UpdateCells: &sheets.UpdateCellsRequest{
			Start: &sheets.GridCoordinate{
				SheetId:     sheetnum,
				RowIndex:    int64(row),
				ColumnIndex: 14, // O, zero based
			},
			Fields: "userEnteredValue",
			Rows: []*sheets.RowData{{Values: []*sheets.CellData{{
				UserEnteredValue: &sheets.ExtendedValue{NumberValue: float64(item.Amount / 100)},
			}}}},
		}})
		delete(items, sku)
	}

	// If there are remaining SKUs that we didn't see, append them to the
	// bottom of the sheet.
	for _, item := range items {
		var skudata *stripe.SKU
		var processor string
		var qty sheets.ExtendedValue
		var price sheets.ExtendedValue

		if skudata, err = getSKU(item.Parent); err != nil {
			goto ERROR
		}
		processor = "Stripe"
		if pt := order.Metadata["payment-type"]; pt != "" {
			processor = "Stripe " + pt
		}
		if order.Shipping == nil {
			var cn string
			if cn, err = getCustomerName(order.Customer.ID); err != nil {
				goto ERROR
			}
			order.Shipping = &stripe.Shipping{Name: cn, Address: &stripe.Address{}}
		}
		if item.Parent != "donation" {
			qty.NumberValue = float64(item.Quantity)
			price.NumberValue = float64(skudata.Price / 100)
		}

		requests = append(requests, &sheets.Request{AppendCells: &sheets.AppendCellsRequest{
			SheetId: sheetnum,
			Fields:  "userEnteredValue",
			Rows: []*sheets.RowData{{Values: []*sheets.CellData{{
				UserEnteredValue: &sheets.ExtendedValue{NumberValue: float64(onum)},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: time.Unix(order.Created, 0).In(time.Local).Format("2016-01-02 15:04:05")},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: processor},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: order.ID},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: order.Shipping.Name},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: order.Email},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: order.Shipping.Address.Line1},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: order.Shipping.Address.City},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: order.Shipping.Address.State},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: order.Shipping.Address.PostalCode},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: skudata.Product.ID},
			}, {
				UserEnteredValue: &sheets.ExtendedValue{StringValue: item.Parent},
			}, {
				UserEnteredValue: &qty,
			}, {
				UserEnteredValue: &price,
			}, {
				UserEnteredValue: &sheets.ExtendedValue{NumberValue: float64(item.Amount / 100)},
			}}}},
		}})
	}

	// Execute the batched change.
	if len(requests) != 0 {
		_, err = svc.Spreadsheets.BatchUpdate(sheet, &sheets.BatchUpdateSpreadsheetRequest{Requests: requests}).Do()
		if err != nil {
			goto ERROR
		}
	}

	// We're happy.
	w.WriteHeader(http.StatusOK)
	return

ERROR:
	belog.Log("%s", err)
	w.WriteHeader(http.StatusInternalServerError)
}

// skus is a cache of SKU data retrieved from Stripe.
var skus = map[string]*stripe.SKU{}

// getSKU returns the cached data for a SKU, fetching it from Stripe if needed.
func getSKU(id string) (s *stripe.SKU, err error) {
	if s = skus[id]; s != nil {
		return s, nil
	}
	if s, err = sku.Get(id, nil); err != nil {
		return nil, err
	}
	skus[id] = s
	return s, nil
}

// customerNames is a cache of customer names retrieved from Stripe.
var customerNames = map[string]string{}

// getCustomerName returns the name of the customer with the specified ID.
func getCustomerName(id string) (name string, err error) {
	var cust *stripe.Customer
	var ok bool

	if name, ok = customerNames[id]; ok {
		return name, nil
	}
	if cust, err = customer.Get(id, nil); err != nil {
		return "", err
	}
	customerNames[id] = cust.Description
	return cust.Description, nil
}
