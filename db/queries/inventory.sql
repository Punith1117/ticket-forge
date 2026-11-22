-- name: CreateInventory :exec
INSERT INTO inventory (
    ticket_category_id
) VALUES (
    $1
);

-- name: GetTicketCategoryInventory :one
SELECT
    tc.id AS ticket_category_id,
    tc.event_id,
    tc.name,
    tc.price_minor,
    tc.currency,
    tc.capacity,
    tc.is_active,
    i.reserved_quantity,
    i.sold_quantity,
    tc.capacity - i.reserved_quantity - i.sold_quantity
        AS available_quantity
FROM ticket_categories AS tc
JOIN inventory AS i
    ON i.ticket_category_id = tc.id
WHERE tc.id = @ticket_category_id;

