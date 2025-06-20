package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

var client *whatsmeow.Client

const groupJIDStr = "1234567890-123456789@g.us" // Replace with your group JID

var groupJID = types.NewJID(groupJIDStr, "g.us")

func openSqliteDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	dbPath := "./store.db"
	db, err := openSqliteDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to open SQLite DB: %v", err)
	}

	dbLogger := waLog.Stdout("SQLSTORE", "INFO", true)

	container := sqlstore.NewWithDB(db, "sqlite", dbLogger)
	if err != nil {
		log.Fatalf("Failed to create SQL store container: %v", err)
	}

	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		log.Fatalf("Failed to get device: %v", err)
	}

	clientLogger := waLog.Stdout("CLIENT", "INFO", true)
	client = whatsmeow.NewClient(device, clientLogger)

	if client.Store.ID == nil {
		log.Println("No session found, please scan QR code to login:")
		qrChan, _ := client.GetQRChannel(ctx)
		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				} else if evt.Event == "success" {
					log.Println("✅ Login successful")
				} else if evt.Event == "timeout" {
					log.Println("QR code timeout, please restart")
				}
			}
		}()

		if err := client.Connect(); err != nil {
			log.Fatalf("Failed to connect client: %v", err)
		}
	} else {
		if err := client.Connect(); err != nil {
			log.Fatalf("Failed to reconnect client: %v", err)
		}
		log.Println("✅ Reconnected to WhatsApp")
	}

	http.HandleFunc("/health", wrap(handleHealth))
	http.HandleFunc("/group/members", wrap(handleListMembers))
	http.HandleFunc("/group/add", wrap(handleAddMember))
	http.HandleFunc("/group/remove", wrap(handleRemoveMember))
	http.HandleFunc("/group/send_contact", wrap(handleSendContact))

	log.Println("HTTP server listening on :8080")
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down...")
	client.Disconnect()
}

func wrap(h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("Panic: %v", rec)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()
		h(w, r)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if client.IsConnected() {
		w.Write([]byte("Connected"))
	} else {
		http.Error(w, "Not connected", http.StatusServiceUnavailable)
	}
}

func handleListMembers(w http.ResponseWriter, r *http.Request) {
	info, err := client.GetGroupInfo(groupJID)
	if err != nil {
		http.Error(w, "Failed to get group info: "+err.Error(), http.StatusInternalServerError)
		return
	}

	members := make([]string, 0, len(info.Participants))
	for _, p := range info.Participants {
		members = append(members, p.JID.User)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}

func handleAddMember(w http.ResponseWriter, r *http.Request) {
	phone := r.URL.Query().Get("phone")
	if phone == "" {
		http.Error(w, "Missing phone parameter", http.StatusBadRequest)
		return
	}
	jid := types.NewJID(phone, "s.whatsapp.net")

	_, err := client.UpdateGroupParticipants(groupJID, []types.JID{jid}, whatsmeow.ParticipantChangeAdd)
	if err != nil {
		http.Error(w, "Failed to add member: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Member added"))
}

func handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	phone := r.URL.Query().Get("phone")
	if phone == "" {
		http.Error(w, "Missing phone parameter", http.StatusBadRequest)
		return
	}
	jid := types.NewJID(phone, "s.whatsapp.net")

	_, err := client.UpdateGroupParticipants(groupJID, []types.JID{jid}, whatsmeow.ParticipantChangeRemove)
	if err != nil {
		http.Error(w, "Failed to remove member: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Member removed"))
}

func handleSendContact(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	phone := r.URL.Query().Get("phone")
	if name == "" || phone == "" {
		http.Error(w, "Missing name or phone parameter", http.StatusBadRequest)
		return
	}

	vcard := fmt.Sprintf(`BEGIN:VCARD
VERSION:3.0
FN:%s
TEL;TYPE=CELL:%s
END:VCARD`, name, phone)

	msg := &waE2E.Message{
		ContactMessage: &waE2E.ContactMessage{
			DisplayName: &name,
			Vcard:       &vcard,
		},
	}

	if _, err := client.SendMessage(context.Background(), groupJID, msg); err != nil {
		http.Error(w, "Failed to send contact: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Contact sent"))
}
