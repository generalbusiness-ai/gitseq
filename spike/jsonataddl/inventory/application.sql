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

CREATE TABLE stock (
    sku TEXT PRIMARY KEY,
    available INTEGER NOT NULL CHECK (available >= 0)
);

CREATE TABLE reservations (
    id TEXT PRIMARY KEY,
    sku TEXT NOT NULL,
    qty INTEGER NOT NULL CHECK (qty > 0)
);

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
