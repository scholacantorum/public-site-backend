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
	var vr *sheets.ValueRange
	var onum int
	var row int
	var cnt int
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

	// Get the list of order numbers from the first column.
	if vr, err = svc.Spreadsheets.Values.Get(sheet, "Orders!A:A").Do(); err != nil {
		goto ERROR
	}

	// Find the rows that contain this order, if any; or the place where
	// rows for this order should be inserted.
	row, cnt = findOrderRows(vr, onum)

	// Update those rows.
	if err = updateRowsForOrder(svc, sheet, sheetnum, &order, onum, row, cnt); err != nil {
		goto ERROR
	}

	// We're happy.
	w.WriteHeader(http.StatusOK)
	return

ERROR:
	belog.Log("%s", err)
	w.WriteHeader(http.StatusInternalServerError)
}

// Find the existing rows for an order, or the place where it should be
// inserted.  The return is the starting row number (zero-indexed), and the
// number of existing rows for the order at that point.
func findOrderRows(vr *sheets.ValueRange, onum int) (row, cnt int) {
	var r []interface{}
	var rowonum int
	var err error

	for row, r = range vr.Values {
		if len(r) < 1 {
			continue
		}
		if rowonum, err = strconv.Atoi(r[0].(string)); err != nil {
			continue
		}
		if rowonum > onum {
			return row, 0 // insert before this row
		}
		if rowonum == onum {
			break
		}
	}
	if rowonum != onum {
		return row + 1, 0 // insert at end
	}
	for cnt = 1; row+cnt < len(vr.Values); cnt++ {
		if len(vr.Values[row+cnt]) < 1 {
			break
		}
		if rowonum, err = strconv.Atoi(vr.Values[row+cnt][0].(string)); err != nil {
			break
		}
		if rowonum != onum {
			break
		}
	}
	return row, cnt
}

// Update the rows for an order.
func updateRowsForOrder(svc *sheets.Service, sheet string, sheetnum int64, o *stripe.Order, onum, row, cnt int) (err error) {
	var items []*stripe.OrderItem
	var sku *stripe.SKU

	// We want one row for each item, but we don't want Stripe's artificial
	// items for taxes and shipping.
	for _, i := range o.Items {
		if i.Type == "tax" || i.Type == "shipping" {
			continue
		}
		items = append(items, i)
	}

	// We also want one row for each returned item, with negative quantities
	// and amounts.
	for _, r := range o.Returns.Data {
		for _, i := range r.Items {
			if i.Type == "tax" || i.Type == "shipping" {
				continue
			}
			i.Quantity = -i.Quantity
			i.Amount = -i.Amount
			items = append(items, i)
		}
	}

	// If there aren't enough rows for this order in the spreadsheet, insert
	// some.  If there are too many, remove some.  In either case, work from
	// the end of whatever's there in hopes of minimal interference with
	// office notes added by the office staff.
	var requests []*sheets.Request
	if cnt < len(items) {
		requests = append(requests, &sheets.Request{InsertDimension: &sheets.InsertDimensionRequest{
			Range: &sheets.DimensionRange{
				SheetId:    sheetnum,
				Dimension:  "ROWS",
				StartIndex: int64(row + cnt),
				EndIndex:   int64(row + len(items)),
			},
			InheritFromBefore: true,
		}})
	} else if cnt > len(items) {
		requests = append(requests, &sheets.Request{DeleteDimension: &sheets.DeleteDimensionRequest{
			Range: &sheets.DimensionRange{
				SheetId:    sheetnum,
				Dimension:  "ROWS",
				StartIndex: int64(row + len(items) - 1),
				EndIndex:   int64(row + cnt - 1),
			}},
		})
	}

	// Fill in the row data for each item.
	for _, i := range items {
		ucr := &sheets.UpdateCellsRequest{
			Fields: "userEnteredValue",
			Start:  &sheets.GridCoordinate{SheetId: sheetnum, RowIndex: int64(row), ColumnIndex: 0},
			Rows:   []*sheets.RowData{&sheets.RowData{Values: nil}}}

		// Column A:  Order Number
		addValue(ucr, onum)

		// Column B: OrderTimestamp
		addValue(ucr, time.Unix(o.Created, 0))

		// Column C: Processor
		if pt := o.Metadata["payment-type"]; pt != "" {
			addValue(ucr, "Stripe "+pt)
		} else {
			addValue(ucr, "Stripe")
		}

		// Column D: ProcessorOrderNumber
		addValue(ucr, o.ID)

		// Columns EFGHIJ: PatronName, PatronEmail, PatronAddress, PatronCity, PatronState, PatronZip
		addValue(ucr, o.Shipping.Name)
		addValue(ucr, o.Email)
		addValue(ucr, o.Shipping.Address.Line1)
		addValue(ucr, o.Shipping.Address.City)
		addValue(ucr, o.Shipping.Address.State)
		addValue(ucr, o.Shipping.Address.PostalCode)

		// Columns KL: Product, SKU
		if sku, err = getSKU(i.Parent); err != nil {
			return err
		}
		addValue(ucr, sku.Product.ID)
		addValue(ucr, sku.ID)

		// Columns MNO: Quantity, Price, Total
		if sku.ID == "donation" {
			addValue(ucr, nil)
			addValue(ucr, nil)
		} else {
			addValue(ucr, i.Quantity)
			addValue(ucr, sku.Price/100)
		}
		addValue(ucr, i.Amount/100)

		requests = append(requests, &sheets.Request{UpdateCells: ucr})
		row++
	}

	// Run the batch update for this order.
	_, err = svc.Spreadsheets.BatchUpdate(sheet, &sheets.BatchUpdateSpreadsheetRequest{Requests: requests}).Do()
	if err != nil {
		return err
	}
	return nil
}

// addValue is a helper that adds a cell value to an UpdateCellsRequest.
func addValue(ucr *sheets.UpdateCellsRequest, v interface{}) {
	var ev sheets.ExtendedValue
	switch v := v.(type) {
	case nil:
		break
	case int:
		ev.NumberValue = float64(v)
	case int64:
		ev.NumberValue = float64(v)
	case string:
		ev.StringValue = v
	case time.Time:
		ev.StringValue = v.In(time.Local).Format("2006-01-02 15:04:05")
	default:
		panic(v)
	}
	ucr.Rows[0].Values = append(ucr.Rows[0].Values, &sheets.CellData{UserEnteredValue: &ev})
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
