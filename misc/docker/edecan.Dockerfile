FROM alpine:3.23 AS runtime

# TARGETPLATFORM est renseigné automatiquement par docker buildx (dockers_v2) ;
# goreleaser range les binaires pré-compilés par plateforme dans le contexte
# (ex: linux/amd64/edecan, linux/arm64/edecan).
ARG TARGETPLATFORM

RUN apk add \
    ca-certificates \
    tzdata \
  && update-ca-certificates

COPY $TARGETPLATFORM/edecan /usr/local/bin/edecan

# edecán lit sa configuration au démarrage : le fichier config.yaml et la base
# SQLite sont attendus dans le volume /data (fourni par l'opérateur, cf. dokku
# storage:mount). Les secrets référencés en ${VAR} dans le YAML proviennent des
# variables d'environnement réelles (dokku config:set), prioritaires sur .env.
VOLUME /data

EXPOSE 8080

CMD ["/usr/local/bin/edecan", "-config", "/data/config.yaml"]
