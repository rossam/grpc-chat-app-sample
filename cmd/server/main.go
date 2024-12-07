package main

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"fmt"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"log"
	"net"

	"google.golang.org/grpc"
	pb "grpc-chat-app-sample/gen/api/chat"
)

const (
	port               = ":8080"
	projectID          = "grpc-chat-app"
	collectionUsers    = "users"
	collectionMessages = "messages"
)

type ChatServiceServer struct {
	pb.UnimplementedChatServiceServer
	firestoreClient *firestore.Client
}

func NewChatServiceServer(client *firestore.Client) *ChatServiceServer {
	return &ChatServiceServer{
		firestoreClient: client,
	}
}

func (s *ChatServiceServer) GetChatMessages(ctx context.Context, req *pb.ChatRequest) (*pb.ChatResponse, error) {
	if s.firestoreClient == nil {
		return nil, status.Error(codes.Internal, "Firestore client is not initialized")
	}
	
	// Firestoreのコレクション一覧を取得してログ出力
	log.Println("Fetching all Firestore collections for debugging...")
	iter := s.firestoreClient.Collections(ctx)
	for {
		col, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			log.Printf("Error fetching Firestore collections: %v", err)
			break
		}
		log.Printf("Firestore collection: %s", col.ID)
	}

	log.Printf("Fetching user data for UserID: %s", req.UserId)
	userDoc, err := s.firestoreClient.Collection(collectionUsers).Doc(req.UserId).Get(ctx)

	if err != nil {

		if status.Code(err) == codes.NotFound {
			log.Printf("UserID %s not found in Firestore", req.UserId)
			return nil, status.Errorf(codes.NotFound, "User not found: %s", req.UserId)
		}
		log.Printf("Error fetching user document: %v", err)
		return nil, status.Errorf(codes.Internal, "Error fetching user document: %v", err)
	}

	suffix, ok := userDoc.Data()["suffixType"].(string)
	if !ok {
		log.Printf("Invalid suffixType for UserID: %s, defaulting to empty", req.UserId)
		suffix = ""
	}

	log.Println("Fetching unread messages...")
	messagesQuery := s.firestoreClient.Collection(collectionMessages).
		Where("read", "!=", true)
	messagesSnapshot, err := messagesQuery.Documents(ctx).GetAll()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to fetch messages: %v", err)
	}

	if len(messagesSnapshot) == 0 {
		log.Println("No new messages found")
		return &pb.ChatResponse{Messages: []string{}}, nil
	}

	var updatedMessages []string
	bulkWriter := s.firestoreClient.BulkWriter(ctx)
	for _, doc := range messagesSnapshot {
		messageData := doc.Data()
		messageText, ok := messageData["message"].(string)
		if !ok {
			log.Printf("Invalid message format in document: %s", doc.Ref.ID)
			continue
		}
		updatedMessages = append(updatedMessages, fmt.Sprintf("%s%s", messageText, suffix))

		_, err := bulkWriter.Update(doc.Ref, []firestore.Update{{Path: "read", Value: true}})
		if err != nil {
			log.Printf("Error queueing read update for document %s: %v", doc.Ref.ID, err)
		}
	}

	bulkWriter.Flush()

	log.Printf("Successfully fetched and updated %d messages", len(updatedMessages))
	return &pb.ChatResponse{Messages: updatedMessages}, nil
}

func main() {
	ctx := context.Background()

	log.Println("Initializing Firestore client...")
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to initialize Firestore client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Fatalf("Failed to close Firestore client: %v", err)
		}
	}()

	log.Println("Starting gRPC server...")
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("Failed to initialize listener: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterChatServiceServer(s, NewChatServiceServer(client))
	reflection.Register(s)

	log.Printf("gRPC server listening on %s", port)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}
}
