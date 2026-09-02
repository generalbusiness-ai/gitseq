CREATE EVENT stock_received (
    id TEXT NOT NULL,
    sku TEXT NOT NULL,
    qty INTEGER NOT NULL CHECK (qty > 0)
);

CREATE EVENT reservation_requested (
    id TEXT NOT NULL,
    sku TEXT NOT NULL,
    qty INTEGER NOT NULL CHECK (qty > 0)
);

CREATE EVENT reservation_cancelled (
    id TEXT NOT NULL
);

CREATE TABLE stock (
    sku TEXT PRIMARY KEY,
    available INTEGER NOT NULL CHECK (available >= 0)
);

CREATE TABLE reservations (
    id TEXT PRIMARY KEY,
    sku TEXT NOT NULL,
    qty INTEGER NOT NULL CHECK (qty > 0)
);

CREATE VIEW reserved_totals AS
    SELECT sku, sum(qty) AS reserved
    FROM reservations
    GROUP BY sku;

CREATE VIEW sku_summary AS
    SELECT s.sku, s.available, coalesce(r.reserved, 0) AS reserved
    FROM stock s LEFT JOIN reserved_totals r ON r.sku = s.sku;

CREATE FOLD receive_stock ON stock_received
READ stock_row OPTIONAL ONE AS
    SELECT sku, available FROM stock WHERE sku = :event.sku
USING 'folds/inventory.jsonata'
WRITES stock;

CREATE FOLD reserve_stock ON reservation_requested
READ stock_row OPTIONAL ONE AS
    SELECT sku, available FROM stock WHERE sku = :event.sku
USING 'folds/inventory.jsonata'
WRITES stock, reservations;

CREATE FOLD cancel_reservation ON reservation_cancelled
READ reservation OPTIONAL ONE AS
    SELECT id, sku, qty FROM reservations WHERE id = :event.id
USING 'folds/cancel.jsonata'
WRITES reservations;
