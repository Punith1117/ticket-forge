-- TicketForge: Deterministic Development Seed Data for Platform-Managed Venues
-- Idempotent upsert by slug

INSERT INTO venues (
    id,
    name,
    slug,
    street_address,
    city,
    state,
    postal_code,
    country_code,
    timezone,
    capacity,
    is_active
) VALUES
(
    '018f0000-0000-7000-8000-000000000001',
    'Kanteerava Indoor Stadium',
    'kanteerava-indoor-stadium',
    'Kasturba Road, Sampangi Rama Nagar',
    'Bengaluru',
    'Karnataka',
    '560001',
    'IN',
    'Asia/Kolkata',
    4000,
    true
),
(
    '018f0000-0000-7000-8000-000000000002',
    'Chowdiah Memorial Hall',
    'chowdiah-memorial-hall',
    '16th Cross, GD Park Extension, Vyalikaval',
    'Bengaluru',
    'Karnataka',
    '560003',
    'IN',
    'Asia/Kolkata',
    1000,
    true
),
(
    '018f0000-0000-7000-8000-000000000003',
    'Shanmukhananda Chandrasekarendra Saraswathi Auditorium',
    'shanmukhananda-auditorium',
    'Comrade Harbanslal Marg, Sion East',
    'Mumbai',
    'Maharashtra',
    '400022',
    'IN',
    'Asia/Kolkata',
    2750,
    true
),
(
    '018f0000-0000-7000-8000-000000000004',
    'NSCI Dome',
    'nsci-dome',
    'Lala Lajpatrai Marg, Lotus Colony, Worli',
    'Mumbai',
    'Maharashtra',
    '400018',
    'IN',
    'Asia/Kolkata',
    5000,
    true
),
(
    '018f0000-0000-7000-8000-000000000005',
    'Siri Fort Auditorium',
    'siri-fort-auditorium',
    'August Kranti Marg, Siri Fort Institutional Area',
    'New Delhi',
    'Delhi',
    '110049',
    'IN',
    'Asia/Kolkata',
    1800,
    true
),
(
    '018f0000-0000-7000-8000-000000000006',
    'Shilpakala Vedika',
    'shilpakala-vedika',
    'Hitech City Main Rd, HITEC City',
    'Hyderabad',
    'Telangana',
    '500081',
    'IN',
    'Asia/Kolkata',
    2500,
    true
)
ON CONFLICT (slug) DO UPDATE SET
    name = EXCLUDED.name,
    street_address = EXCLUDED.street_address,
    city = EXCLUDED.city,
    state = EXCLUDED.state,
    postal_code = EXCLUDED.postal_code,
    country_code = EXCLUDED.country_code,
    timezone = EXCLUDED.timezone,
    capacity = EXCLUDED.capacity,
    is_active = EXCLUDED.is_active,
    updated_at = now();
