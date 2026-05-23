-- +goose Up

INSERT INTO substances (label, toxicity) VALUES
    ('Confidor Energy',    'T+'),
    ('Actara 25 WG',       'T+'),
    ('Mospilan 20 SG',     'T'),
    ('Karate Zeon',        'T'),
    ('Calypso 480 SC',     'T'),
    ('Decis Mega',         'T-'),
    ('Fastac Active',      'T-'),
    ('Mavrik Aqua',        'T-'),
    ('Bulldock 25 EC',     'T+'),
    ('Nurelle D',          'T')
ON CONFLICT (label) DO NOTHING;

-- +goose Down

DELETE FROM substances WHERE label IN (
    'Confidor Energy', 'Actara 25 WG', 'Mospilan 20 SG', 'Karate Zeon',
    'Calypso 480 SC', 'Decis Mega', 'Fastac Active', 'Mavrik Aqua',
    'Bulldock 25 EC', 'Nurelle D'
);
