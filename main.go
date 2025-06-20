package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
)

var client *whatsmeow.Client

const groupJIDStr = "1234567890-123456789@g.us" // Replace with your actual group ID
var groupJID = types.NewJID(groupJIDStr, "g.us")

func main() {
	dbLog := log.New(os.Stdout, "SQL: ", log.Lshortfile)
	container, err := sqlstore.New("sqlite", "file:store.db?_foreign_keys=on", dbLog)
	if err != nil {
		log.Fatalf("Failed to create store: %v", err)
	}

	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		log.Fatalf("Failed to get device: %v", err)
	}

	client = whatsmeow.NewClient(deviceStore, nil)

	if client.Store.ID == nil {
		log.Println("No session found, scanning QR...")
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else if evt.Event == "success" {
				log.Println("Login successful!")
			} else if evt.Event == "timeout" || evt.Event == "error" {
				log.Fatalf("Login failed: %v", evt.Event)
			}
		}
	} else {
		err = client.Connect()
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		log.Println("Connected with restored session")
	}

	http.HandleFunc("/health", wrap(handleHealth))
	http.HandleFunc("/group/members", wrap(handleListMembers))
	http.HandleFunc("/group/add", wrap(handleAddMember))
	http.HandleFunc("/group/remove", wrap(handleRemoveMember))
	http.HandleFunc("/group/send_contact", wrap(handleSendContact))

	log.Println("Server started at :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func wrap(h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if e := recover(); e != nil {
				log.Printf("panic: %v", e)
				http.Error(w, "Internal server error", 500)
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
		http.Error(w, "Failed to get group info: "+err.Error(), 500)
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
		http.Error(w, "Missing phone param", 400)
		return
	}
	jid := types.NewJID(phone, "s.whatsapp.net")
	err := client.AddGroupParticipants(groupJID, []types.JID{jid})
	if err != nil {
		http.Error(w, "Failed to add: "+err.Error(), 500)
		return
	}
	fmt.Fprintln(w, "Added")
}

func handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	phone := r.URL.Query().Get("phone")
	if phone == "" {
		http.Error(w, "Missing phone param", 400)
		return
	}
	jid := types.NewJID(phone, "s.whatsapp.net")
	err := client.RemoveGroupParticipant(groupJID, jid)
	if err != nil {
		http.Error(w, "Failed to remove: "+err.Error(), 500)
		return
	}
	fmt.Fprintln(w, "Removed")
}

func handleSendContact(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	phone := r.URL.Query().Get("phone")
	if name == "" || phone == "" {
		http.Error(w, "Missing name or phone param", 400)
		return
	}

	vcard := fmt.Sprintf(`BEGIN:VCARD
VERSION:3.0
FN:%s
TEL;TYPE=CELL:%s
END:VCARD`, name, phone)

	msg := &proto.Message{
		ContactMessage: &proto.ContactMessage{
			DisplayName: &name,
			Vcard:       protoString(vcard),
		},
	}

	_, err := client.SendMessage(context.Background(), groupJID, "", msg)
	if err != nil {
		http.Error(w, "Failed to send contact: "+err.Error(), 500)
		return
	}
	fmt.Fprintln(w, "Contact sent")
}

func protoString(s string) *string {
	return &s
}
