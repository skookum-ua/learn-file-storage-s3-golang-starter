package main

import (
	"fmt"
	"io"

	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type header", err)
		return
	}

	if !slices.Contains([]string{"image/jpeg", "image/png"}, mediaType) {
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

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal databaseerror", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User iws not video owner", nil)
		return
	}

	filePath := filepath.Join(cfg.assetsRoot, videoIDString+"."+fileExtention)

	tumbnailFile, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to read file data", err)
		return
	}
	io.Copy(tumbnailFile, file)

	dataUrl := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, videoID, fileExtention)

	video.ThumbnailURL = &dataUrl

	cfg.db.UpdateVideo(video)

	respondWithJSON(w, http.StatusOK, video)
}
