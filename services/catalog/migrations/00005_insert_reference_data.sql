-- +goose Up
INSERT INTO airlines (code, name) VALUES
    ('GA', 'Garuda Indonesia'),
    ('QG', 'Citilink'),
    ('JT', 'Lion Air'),
    ('ID', 'Batik Air'),
    ('IU', 'Super Air Jet'),
    ('QZ', 'Indonesia AirAsia'),
    ('SQ', 'Singapore Airlines'),
    ('NH', 'All Nippon Airways');

INSERT INTO airports (code, name, city, timezone) VALUES
    ('CGK', 'Soekarno-Hatta International Airport', 'Jakarta', 'Asia/Jakarta'),
    ('SUB', 'Juanda International Airport', 'Surabaya', 'Asia/Jakarta'),
    ('KNO', 'Kualanamu International Airport', 'Medan', 'Asia/Jakarta'),
    ('YIA', 'Yogyakarta International Airport', 'Yogyakarta', 'Asia/Jakarta'),
    ('DPS', 'I Gusti Ngurah Rai International Airport', 'Denpasar', 'Asia/Makassar'),
    ('UPG', 'Sultan Hasanuddin International Airport', 'Makassar', 'Asia/Makassar'),
    ('BPN', 'Sultan Aji Muhammad Sulaiman Sepinggan Airport', 'Balikpapan', 'Asia/Makassar'),
    ('DJJ', 'Sentani International Airport', 'Jayapura', 'Asia/Jayapura'),
    ('SIN', 'Changi Airport', 'Singapore', 'Asia/Singapore'),
    ('KUL', 'Kuala Lumpur International Airport', 'Kuala Lumpur', 'Asia/Kuala_Lumpur'),
    ('HND', 'Haneda Airport', 'Tokyo', 'Asia/Tokyo');

-- +goose Down
DELETE FROM airports
WHERE code IN ('CGK', 'SUB', 'KNO', 'YIA', 'DPS', 'UPG', 'BPN', 'DJJ', 'SIN', 'KUL', 'HND');

DELETE FROM airlines
WHERE code IN ('GA', 'QG', 'JT', 'ID', 'IU', 'QZ', 'SQ', 'NH');
