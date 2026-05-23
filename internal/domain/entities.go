package domain

import "time"

type User struct {
	ID           string `json:"id"`
	CNP          string `json:"cnp,omitempty"`
	Name         string `json:"name"`
	FullName     string `json:"full_name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	Role         Role   `json:"role"`
	County       string `json:"county"`
	Locality     string `json:"locality"`
	PasswordHash string `json:"-"`
}

type Apiary struct {
	ID         string     `json:"id"`
	OwnerID    string     `json:"owner_id"`
	Name       string     `json:"name"`
	Type       ApiaryType `json:"type"`
	Lat        float64    `json:"lat"`
	Lng        float64    `json:"lng"`
	HiveCount  int        `json:"hive_count"`
	StartDate  string     `json:"start_date"`
	EndDate    *string    `json:"end_date"`
	Notes      *string    `json:"notes"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Parcel struct {
	ID              string  `json:"id"`
	OwnerID         string  `json:"owner_id"`
	Name            string  `json:"name"`
	CadastralNumber string  `json:"cadastral_number"`
	Lat             float64 `json:"lat"`
	Lng             float64 `json:"lng"`
	SurfaceHA       float64 `json:"surface_ha"`
	DefaultCrop     *string `json:"default_crop"`
	County          string  `json:"county"`
	Locality        string  `json:"locality"`
}

type SprayReport struct {
	ID                   string      `json:"id"`
	FarmerID             string      `json:"farmer_id"`
	ParcelID             string      `json:"parcel_id"`
	Crop                 string      `json:"crop"`
	Substance            string      `json:"substance"`
	Toxicity             Toxicity    `json:"toxicity"`
	SurfaceHA            float64     `json:"surface_ha"`
	ScheduledAt          time.Time   `json:"scheduled_at"`
	DurationHours        float64     `json:"duration_hours"`
	Notes                *string     `json:"notes"`
	Status               SprayStatus `json:"status"`
	AffectedApiariesCount int        `json:"affected_apiaries_count"`
	LedgerHash           string      `json:"ledger_hash"`
	CreatedAt            time.Time   `json:"created_at"`
}

type AlertDispatch struct {
	ID              string      `json:"id"`
	SprayReportID   string      `json:"spray_report_id"`
	BeekeeperID     string      `json:"beekeeper_id"`
	ApiaryID        string      `json:"apiary_id"`
	DistanceM       float64     `json:"distance_m"`
	Downwind        bool        `json:"downwind"`
	PushState       PushState   `json:"push_state"`
	CallState       CallState   `json:"call_state"`
	CallAttempts    int         `json:"call_attempts"`
	SmsState        SmsState    `json:"sms_state"`
	InAppAction     *InAppAction `json:"in_app_action"`
	FinalStatus     *FinalStatus `json:"final_status"`
	TwilioCallSID   *string     `json:"twilio_call_sid,omitempty"`
	TwilioSMSSID    *string     `json:"twilio_sms_sid,omitempty"`
	LedgerHash      string      `json:"ledger_hash"`
	CreatedAt       time.Time   `json:"created_at"`
}

type DamageClaim struct {
	ID            string            `json:"id"`
	BeekeeperID   string            `json:"beekeeper_id"`
	ApiaryID      string            `json:"apiary_id"`
	RelatedSprayID *string          `json:"related_spray_id"`
	Description   string            `json:"description"`
	HiveLossCount int               `json:"hive_loss_count"`
	GpsLat        float64           `json:"gps_lat"`
	GpsLng        float64           `json:"gps_lng"`
	Status        DamageClaimStatus `json:"status"`
	Photos        []string          `json:"photos"`
	LedgerHash    string            `json:"ledger_hash"`
	CreatedAt     time.Time         `json:"created_at"`
}

type LedgerEvent struct {
	ID        string         `json:"id"`
	Hash      string         `json:"hash"`
	PrevHash  *string        `json:"prev_hash"`
	Type      string         `json:"type"`
	ActorID   *string        `json:"actor_id"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

type PushSubscription struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

type WeatherResult struct {
	WindDirectionDeg float64   `json:"wind_direction_deg"`
	WindSpeedMs      float64   `json:"wind_speed_ms"`
	TemperatureC     float64   `json:"temperature_c"`
	FetchedAt        time.Time `json:"fetched_at"`
}

type Substance struct {
	Label    string   `json:"label"`
	Toxicity Toxicity `json:"toxicity"`
}
