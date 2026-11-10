package helptext

// french is the French table. It covers every page this build carries
// English for; a page added to English and not here still reads English, and
// Load warns once with the ids it is missing.
//
// What is not translated, here or in any table: a CLI line, a config path, a
// key label. Those are things a person types, and the shell, the file and the
// keyboard do not read French.
var french = Language{
	Code: "fr",
	Entries: map[string]Entry{
		IDDock: {
			Title: "Dock",
			Purpose: "Le dock est la barre d'état : la pastille de mode, la bande des " +
				"espaces de travail, les panneaux réduits, les commandes de session, les " +
				"notifications et les cellules que la configuration y place. Cliquer sur " +
				"une cellule agit sur la ligne qu'elle nomme.",
			Plural: ucFirst(frPart),
		},
		IDDockCustom: {
			Title: "Cellule du dock : {name}",
			Purpose: "Une cellule de dock personnalisée. Sa commande est exécutée selon la " +
				"règle de rafraîchissement définie pour elle, et un clic lance la commande " +
				"de clic.",
			Plural: ucFirst(frPart),
		},
		IDDockPlugin: {
			Title: "Cellule du dock : plugin",
			Purpose: "Une cellule fournie par un plugin. Le plugin décide de ce qu'elle " +
				"dessine et de ce que fait un clic ; son manifeste et ses cellules sont " +
				"décrits dans sa propre documentation.",
			Plural: ucFirst(frPart),
		},
		IDDockWorkspaces: {
			Title: "Espaces de travail",
			Purpose: "La bande des espaces de travail. Chaque pastille est un espace de " +
				"travail et ses panneaux ; un clic bascule vers lui, et les flèches font " +
				"défiler une bande trop longue pour le dock.",
			Plural: ucFirst(frPart),
		},
		IDDockWindows: {
			Title: "Panneaux réduits",
			Purpose: "Une entrée par panneau réduit. Un clic sur une entrée restaure ce " +
				"panneau ; le marqueur de débordement ouvre la liste des entrées que le " +
				"dock n'a pas pu loger.",
			Plural: ucFirst(frPart),
		},
		IDDockSessionPanel: {
			Title: "Commandes de session",
			Purpose: "Les commandes du client : ouvrir la liste des sessions, quitter ce " +
				"client (se détacher), fermer la session ou en démarrer une autre. Elles " +
				"agissent sur la session du démon, pas sur un panneau.",
			Plural: ucFirst(frPart),
		},
		IDDockMessages: {
			Title: "Messages",
			Purpose: "La notification la plus récente. Entrée ou un clic agit dessus, échap " +
				"la ferme, et un message coupé peut être maintenu pour lire la suite. Les " +
				"notifications portent aussi les demandes des agents.",
			Plural: ucFirst(frPart),
		},
		IDDockOverflow: {
			Title: "Débordement des panneaux réduits",
			Purpose: "Le marqueur des panneaux réduits que le dock n'a pas pu loger. Il " +
				"ouvre la liste agrégée, qui les nomme tous.",
			Plural: ucFirst(frPart),
		},
		IDDockClock: {
			Title: "Horloge",
			Purpose: "L'heure courante, dessinée par le dock. Son format et sa présence " +
				"sont des réglages d'apparence.",
			Plural: ucFirst(frPart),
		},
		IDDockCPU: {
			Title: "CPU",
			Purpose: "La mesure du processeur. Elle échantillonne la machine tant que le " +
				"dock est affiché ; désactivez-la quand le dock n'est pas l'endroit où " +
				"vous surveillez la charge.",
			Plural: ucFirst(frPart),
		},
		IDDockRAM: {
			Title: "RAM",
			Purpose: "La mesure de la mémoire, dans les unités dont le dock se sert pour " +
				"le total de la machine. C'est une cellule du dock, pas un chiffre par " +
				"panneau.",
			Plural: ucFirst(frPart),
		},
		IDDockTransfers: {
			Title: "Transferts",
			Purpose: "La bande des tampons de copie et de collage : ce qui est retenu, et " +
				"si un transfert est en cours. Un clic ouvre la liste des tampons.",
			Plural: ucFirst(frPart),
		},
		IDDockSession: {
			Title: "Nom de la session",
			Purpose: "La session du démon que ce client affiche, sa couleur d'accent et si " +
				"d'autres clients y sont attachés. Un clic ouvre le sélecteur de sessions.",
			Plural: ucFirst(frPart),
		},
		IDDockMode: {
			Title: "Mode",
			Purpose: "Le mode de saisie : gestion des fenêtres ou terminal. C'est la " +
				"réponse du dock à la question de savoir où ira une touche, et il change " +
				"quand le menu de préfixe s'ouvre.",
			Plural: ucFirst(frPart),
		},
		IDDockTrail: {
			Title: "Piste",
			Purpose: "La cellule de piste retient où ce client est passé : les panneaux et " +
				"les espaces de travail visités, du plus récent au plus ancien. Elle est " +
				"intégrée au dock et n'exécute pas de commande.",
			Plural: ucFirst(frPart),
		},
		IDDockTape: {
			Title: "Tape",
			Purpose: "La cellule de tape signale une tape en cours dans cette session, avec " +
				"son état. Elle n'est dessinée que pendant la lecture d'une tape.",
			Plural: ucFirst(frPart),
		},
		IDDockNotifications: {
			Title: "Notifications",
			Purpose: "La cellule de notification porte le message le plus récent et son " +
				"niveau. Sa durée et le maintien des erreurs jusqu'à leur fermeture sont " +
				"des réglages de notification.",
			Plural: ucFirst(frPart),
		},
		IDDockCopyHelp: {
			Title: "Aide au copie",
			Purpose: "La cellule d'aide au copie rappelle dans le dock la combinaison du " +
				"mode copie tant que celui-ci est disponible. C'est de la documentation, " +
				"pas une commande.",
			Plural: ucFirst(frPart),
		},
	},
}

// frPart is the count after a component's name: French inflects at two, so 0
// and 1 are singular and every other number is plural.
func frPart(n int) string {
	if n < 2 {
		return "partie"
	}
	return "parties"
}

// ucFirst capitalises a part label for the place the engine writes it: after
// a name, the count reads as a sentence of its own. The words are ASCII, so
// the first byte is a letter.
func ucFirst(f func(int) string) func(int) string {
	return func(n int) string {
		s := f(n)
		if s == "" {
			return s
		}
		return string(s[0]-'a'+'A') + s[1:]
	}
}
