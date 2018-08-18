# Order Processing Back End

This directory contains the server-side program that handles order processing.
It is invoked as a CGI script when a customer places an order (through the URL
/orders/backend).  The CGI script is invoked with a form, whose parameters are:

* `name`: customer name
* `email`: customer email
* `address`, `city`, `state`, `zip`: customer's billing address
* `product`: code for product being ordered
* `qty`: quantity being ordered
* `token`: a Stripe token, standing in place for the customer's payment
  information.

If the order is processed successfully, the response will have status code 200
and no body.  If the order has a problem, the response will have status code
400 and a text/plain body with an error message.

Along the success path, order processing consists of the following steps:

1.  Assign a unique order number.

    The last-used order number is kept in a plain file on the server, with file
    locking to prevent collisions.

2.  Log the order details (just in case).

    The order details are appended to a plain text file on the server, as a
    backup.

3.  Find or create the customer in Stripe.

4.  Submit the charge to Stripe.  Include all details so that everything can be
    rebuilt by reading Stripe data if needed.  Order number, qty, desc in
    description; order number, qty, product in metadata.

5.  Send a receipt to the customer with a copy to the office.

