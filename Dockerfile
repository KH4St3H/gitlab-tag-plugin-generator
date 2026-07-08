FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gitlab-tag-plugin-generator .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gitlab-tag-plugin-generator /gitlab-tag-plugin-generator
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/gitlab-tag-plugin-generator"]
