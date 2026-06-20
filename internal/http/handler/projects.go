package handler

import (
	"context"
	"fmt"
	"net/http"

	"edecan/internal/core/model"
	"edecan/internal/http/view/page"
)

// defaultProjectSlug retourne le slug du projet sur lequel rediriger
// l'utilisateur : son dernier projet actif (session la plus récemment mise
// à jour, tous projets confondus) ou, à défaut, son premier projet
// accessible. Retourne "" si l'utilisateur n'appartient à aucun projet.
func (h *Handlers) defaultProjectSlug(ctx context.Context, user *model.User) (string, error) {
	accesses := h.Registry.ProjectsForEmail(user.Email)
	if len(accesses) == 0 {
		return "", nil
	}

	lastProjectID, ok, err := h.ChatService.LastVisitedProject(ctx, user.ID)
	if err != nil {
		return "", err
	}
	if ok {
		for _, a := range accesses {
			if a.Project.ID == lastProjectID {
				return string(a.Project.ID), nil
			}
		}
	}
	return string(accesses[0].Project.ID), nil
}

// Home redirige vers les sessions du dernier projet actif de l'utilisateur
// (ou son premier projet accessible) — il n'y a pas de page d'accueil
// dédiée.
func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	ctx := r.Context()

	slug, err := h.defaultProjectSlug(ctx, user)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if slug == "" {
		render(w, r, page.NoProjectAccess(user.Email))
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%s/chat", slug), http.StatusSeeOther)
}
