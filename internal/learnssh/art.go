package learnssh

// logo is the tuios wordmark, as the fake shell's neofetch draws it.
var logo = []string{
	`████████╗██╗   ██╗██╗ ██████╗ ███████╗`,
	`╚══██╔══╝██║   ██║██║██╔═══██╗██╔════╝`,
	`   ██║   ██║   ██║██║██║   ██║███████╗`,
	`   ██║   ██║   ██║██║██║   ██║╚════██║`,
	`   ██║   ╚██████╔╝██║╚██████╔╝███████║`,
	`   ╚═╝    ╚═════╝ ╚═╝ ╚═════╝ ╚══════╝`,
}

// logoColors run down the wordmark, top to bottom.
var logoColors = []string{"#f5a3ff", "#d99bff", "#b894ff", "#958dff", "#7a8bff", "#5fa8ff"}

// wizard is the finale.
var wizard = []string{
	`              *        .          *`,
	`       .            /\        .`,
	`   *          .    /  \    *        .`,
	`                  / *  \       *`,
	`       .         /  .   \   .`,
	`           *    /________\        *`,
	`               (_  o  o  _)`,
	`         .       \  --  /     .`,
	`                  '----'`,
	`            ~~~~~~~~~~~~~~~~~~~~`,
}

// Install commands on the finale screen.
var installLines = []string{
	"brew install tuios",
	"curl -fsSL https://raw.githubusercontent.com/Gaurav-Gosain/tuios/main/install.sh | bash",
	"go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest",
}
