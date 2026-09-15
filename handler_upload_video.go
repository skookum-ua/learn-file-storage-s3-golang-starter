package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal databaseerror", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User iws not video owner", nil)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}

	if !slices.Contains([]string{"video/mp4"}, mediaType) {
		respondWithError(w, http.StatusBadRequest, "Not allowed file extension", nil)
		return
	}

	var fileExtention string
	if len(strings.Split(mediaType, "/")) == 2 {
		fileExtention = strings.Split(mediaType, "/")[1]
	} else {
		respondWithError(w, http.StatusBadRequest, "Not allowed file extension?", nil)
		return
	}

	randByte := make([]byte, 32)
	rand.Read(randByte)
	randByteString := base64.RawURLEncoding.EncodeToString(randByte)

	f, err := os.CreateTemp("", "tubely-upload."+fileExtention)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Culdn`t create temp file", err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()

	_, err = io.Copy(f, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Culdn`t write temp file", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(f.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get aspect ratio", err)
		return
	}

	processedPath, err := processVideoForFastStart(f.Name())

	newFile, err:= os.Open(processedPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Culdn`t create temp file", err)
		return
	}

	defer newFile.Close()

	newFile.Seek(0, io.SeekStart)

	key := fmt.Sprintf("%s.%s", randByteString, fileExtention)
	key = filepath.Join(aspectRatio, key)

	input := &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        newFile,
		ContentType: aws.String(mediaType),
	}

	_, err = cfg.s3Client.PutObject(r.Context(), input)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't upload video to object storage", err)
		return
	}

	videoURL := fmt.Sprintf("%s/%s", cfg.s3CfDistribution, key)

	video.VideoURL = &videoURL

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func getVideoAspectRatio(filePath string) (string, error) {
	command := exec.Command(
		"ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams",
		filePath,
	)
	var buf bytes.Buffer
	command.Stdout = &buf
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("ffprobe failed: %w", err)
	}
	type dimensions struct {
		Height int `json:"height"`
		Width  int `json:"width"`
	}
	type result struct {
		Streams []dimensions `json:"streams"`
	}
	res := result{}
	json.Unmarshal(buf.Bytes(), &res)
	if len(res.Streams) == 0 {
		return "", fmt.Errorf("no streams found")
	}
	width := res.Streams[0].Width
	height := res.Streams[0].Height
	if width == 0 || height == 0 {
		return "", fmt.Errorf("invalid dimensions")
	}
	ratio := float64(width) / float64(height)
	if ratio > 1.7 && ratio < 1.8 {
		return "landscape", nil
	}
	if ratio > 0.55 && ratio < 0.57 {
		return "portrait", nil
	}
	return "other", nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputPath := filePath + ".processing"
	command := exec.Command(
		"ffmpeg",
		"-i", filePath,
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4",
		outputPath,
	)

	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg failed: %w: %s", err, stderr.String())
	}

	return outputPath, nil
}
