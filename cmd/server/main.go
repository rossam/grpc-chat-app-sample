package main

import (
	// Firestoreを操作するためのクライアントライブラリ
	"cloud.google.com/go/firestore"
	"errors"
	"fmt"

	// コンテキスト処理（タイムアウトやキャンセルなど）を行うための標準パッケージ
	"context"
	// Firestoreのイテレーション処理（反復処理）に必要
	"google.golang.org/api/iterator"
	// gRPCエラーコードを扱うためのライブラリ
	"google.golang.org/grpc/codes"
	// gRPCのエラー処理に必要なライブラリ
	"google.golang.org/grpc/status"
	// ログ出力を行うための標準ライブラリ
	"log"
	// ネットワークのリスナー作成に必要なパッケージ
	"net"
	// gRPCの機能を提供するパッケージ
	"google.golang.org/grpc"
	// gRPCサーバー用のリフレクションを有効にするライブラリ
	"google.golang.org/grpc/reflection"
	// 定義されたプロトコルバッファー（.proto）の自動生成コード
	pb "grpc-chat-app-sample/gen/api/chat"
)

// 定数定義
const (
	// gRPCサーバーのポート番号
	port = ":8080"
	// FirestoreプロジェクトID
	projectID = "grpc-chat-app"
	// ユーザー情報を格納するFirestoreコレクション
	collectionUsers = "users"
	// メッセージ情報を格納するFirestoreコレクション
	collectionMessages = "messages"
)

// gRPCサーバーの構造体定義
type ChatServiceServer struct {
	// 必須: gRPCの実装を埋め込む
	pb.UnimplementedChatServiceServer
	firestoreClient *firestore.Client
}

// サーバーのコンストラクタ
func NewChatServiceServer(client *firestore.Client) *ChatServiceServer {
	return &ChatServiceServer{
		firestoreClient: client,
	}
}

// ユーザーのsuffixTypeを取得し、接尾辞を決定する関数
func (s *ChatServiceServer) getUserSuffix(ctx context.Context, userId string) (string, error) {
	// Firestoreからユーザー情報を取得
	userDoc, err := s.firestoreClient.Collection(collectionUsers).Doc(userId).Get(ctx)
	if err != nil {
		// ユーザーが見つからない場合のエラー処理
		if status.Code(err) == codes.NotFound {
			log.Printf("UserID %s not found in Firestore", userId)
			return "", status.Errorf(codes.NotFound, "User not found: %s", userId)
		}

		log.Printf("Error fetching user document: %v", err)
		return "", status.Errorf(codes.Internal, "Error fetching user document: %v", err)
	}

	// FirestoreからsuffixTypeを取得
	rawSuffix, ok := userDoc.Data()["suffixType"].(string)
	if !ok {
		log.Printf("Invalid suffixType for UserID: %s, defaulting to empty", userId)
		return "", nil
	}

	// suffixTypeに応じた接尾辞を決定
	switch rawSuffix {
	case "猫":
		return "にゃん", nil
	case "犬":
		return "わん", nil
	case "キャラクター":
		return "だよん", nil
	default:
		return "", nil
	}
}

// "read"フィールドが存在しないメッセージにデフォルト値を設定する関数
func updateMessagesWithDefaultRead(ctx context.Context, client *firestore.Client) error {
	// "messages"コレクション内にある"read"フィールドが存在しないメッセージ群を反復処理
	iter := client.Collection(collectionMessages).Where("read", "==", nil).Documents(ctx)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			log.Printf("Error iterating documents: %v", err)
			return err
		}

		// `read`フィールドが存在しない場合、falseを設定
		_, err = doc.Ref.Update(ctx, []firestore.Update{
			{Path: "read", Value: false},
		})

		if err != nil {
			log.Printf("Failed to update document %s: %v", doc.Ref.ID, err)
		} else {
			log.Printf("Document %s updated successfully", doc.Ref.ID)
		}
	}
	return nil
}

// 未読のメッセージをFirestoreから取得する関数
func (s *ChatServiceServer) fetchUnreadMessages(ctx context.Context) ([]*firestore.DocumentSnapshot, error) {
	// Firestoreクエリを作成(Firestoreデータベースに格納されているデータを、特定の条件で検索・取得するための命令のこと)
	// "messages"コレクションから、"read"フィールドがtrue以外のドキュメントを対象とする
	messagesQuery := s.firestoreClient.Collection(collectionMessages).
		Where("read", "!=", true)

	// クエリを実行し、該当するすべてのドキュメントを取得
	// Documents(ctx).GetAll()は、指定したクエリに一致するすべてのドキュメントのスナップショットを取得
	messagesSnapshot, err := messagesQuery.Documents(ctx).GetAll()
	if err != nil {
		log.Printf("Failed to fetch messages: %v", err)
		return nil, status.Errorf(codes.Internal, "Failed to fetch messages: %v", err)
	}

	return messagesSnapshot, nil
}

// メッセージを取得し、未読を既読に更新するメインのgRPCメソッド
func (s *ChatServiceServer) GetChatMessages(ctx context.Context, req *pb.ChatRequest) (*pb.ChatResponse, error) {
	// Firestoreクライアントが初期化されていない場合のエラー処理
	if s.firestoreClient == nil {
		return nil, status.Error(codes.Internal, "Firestore client is not initialized")
	}

	// 既読フィールドがないメッセージに対してデフォルト値を設定
	if err := updateMessagesWithDefaultRead(ctx, s.firestoreClient); err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to update messages: %v", err)
	}

	// ユーザーの接尾辞を取得
	suffix, err := s.getUserSuffix(ctx, req.UserId)
	if err != nil {
		return nil, err
	}

	// 未読メッセージを取得
	messagesSnapshot, err := s.fetchUnreadMessages(ctx)
	if err != nil {
		return nil, err
	}

	// 未読メッセージがない場合の処理
	if len(messagesSnapshot) == 0 {
		log.Println("No new messages found")
		return &pb.ChatResponse{Messages: []string{}}, nil
	}

	// メッセージのリストを作成し、既読に更新
	var updatedMessages []string

	// BulkWriterを作成
	// BulkWriterはFirestoreへの複数の書き込みを効率的に処理するためのツール
	// この場合、メッセージの既読状態を一括で更新するために使用
	bulkWriter := s.firestoreClient.BulkWriter(ctx)
	for _, doc := range messagesSnapshot {
		messageData := doc.Data()
		// もし"message"フィールドが存在しないか、文字列ではない場合、okはfalse
		messageText, ok := messageData["message"].(string)
		if !ok {
			log.Printf("Invalid message format in document: %s", doc.Ref.ID)
			continue
		}

		// ユーザーの接尾辞(suffix)をメッセージの末尾に追加
		// fmt.Sprintfは、フォーマット済みの文字列を作成するための関数
		updatedMessages = append(updatedMessages, fmt.Sprintf("%s%s", messageText, suffix))

		// メッセージを既読に更新
		_, err := bulkWriter.Update(doc.Ref, []firestore.Update{{Path: "read", Value: true}})
		if err != nil {
			log.Printf("Error queueing read update for document %s: %v", doc.Ref.ID, err)
		}
	}

	// BulkWriterをフラッシュして一括更新
	bulkWriter.Flush()

	log.Printf("Successfully fetched and updated %d messages", len(updatedMessages))
	return &pb.ChatResponse{Messages: updatedMessages}, nil
}

// gRPCサーバーのエントリーポイント
func main() {
	// コンテキストを初期化
	// コンテキストは、タイムアウトやキャンセルなどの制御をするために使用
	ctx := context.Background()

	// Firestoreクライアントの初期化
	// Firestoreクライアントは、Firestoreデータベースとやり取りするための重要なオブジェクト
	log.Println("Initializing Firestore client...")
	// projectIDで指定されたプロジェクトに接続
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to initialize Firestore client: %v", err)
	}

	// Firestoreクライアントの終了処理
	// データベースとの接続をクリーンに切断するために必要
	defer func() {
		if err := client.Close(); err != nil {
			log.Fatalf("Failed to close Firestore client: %v", err)
		}
	}()

	// gRPCサーバーの起動
	log.Println("Starting gRPC server...")

	// TCPリスナーを作成
	// クライアントが接続するためのポートを指定
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("Failed to initialize listener: %v", err)
	}

	// gRPCサーバーを作成
	s := grpc.NewServer()

	// gRPCサーバーにサービスを登録
	// NewChatServiceServerはFirestoreクライアントを受け取り、gRPCサービスを初期化
	pb.RegisterChatServiceServer(s, NewChatServiceServer(client))

	// gRPCのリフレクションを有効化
	// リフレクションは、クライアントがサーバーのメソッド情報を動的に取得できる機能
	reflection.Register(s)

	// gRPCサーバーのリッスンを開始
	// クライアントからの接続を受け付ける
	log.Printf("gRPC server listening on %s", port)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}
}
