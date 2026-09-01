#!/usr/bin/env python3
"""
UDP PRO — Interface CLI professionnelle
========================================

CLI Python avec Rich pour la gestion des comptes, offres et licences.
Appelle le binaire Go `labosurf` en arrière-plan.

Usage :
    python labosurf_cli.py
    python labosurf_cli.py create --id client1 --days 30
    python labosurf_cli.py list
    python labosurf_cli.py show --id client1
"""

import subprocess
import sys
import os
import shutil
from pathlib import Path

try:
    from rich.console import Console
    from rich.table import Table
    from rich.panel import Panel
    from rich.text import Text
    from rich.progress import Progress, SpinnerColumn, TextColumn
    from rich.prompt import Prompt, Confirm
    from rich import box
except ImportError:
    print("Installation de Rich en cours...")
    subprocess.check_call([sys.executable, "-m", "pip", "install", "rich", "-q"])
    from rich.console import Console
    from rich.table import Table
    from rich.panel import Panel
    from rich.text import Text
    from rich.progress import Progress, SpinnerColumn, TextColumn
    from rich.prompt import Prompt, Confirm
    from rich import box

console = Console()

# --- Configuration ---
BINARY_NAME = "labosurf"
STORE_FILE = "store.json"
LICENSE_FILE = "license.lic"


def find_binary():
    """Trouve le binaire labosurf."""
    # 1. Même répertoire que le script
    script_dir = Path(__file__).parent
    candidates = [
        script_dir / BINARY_NAME,
        script_dir / f"{BINARY_NAME}.exe",
        script_dir / BINARY_NAME / "labosurf.exe",
    ]
    for c in candidates:
        if c.exists():
            return str(c)

    # 2. PATH système
    found = shutil.which(BINARY_NAME)
    if found:
        return found

    return None


def run_admin(args, store=None):
    """Exécute une commande admin via le binaire Go."""
    binary = find_binary()
    if not binary:
        console.print("[bold red]✘ Binaire labosurf introuvable.[/]")
        console.print("Compilez-le d'abord : [cyan]go build -o labosurf .[/]")
        return None

    cmd = [binary, "admin"] + args
    if store:
        cmd += ["-store", store]

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        return result
    except subprocess.TimeoutExpired:
        console.print("[bold red]✘ Timeout du binaire.[/]")
        return None
    except Exception as e:
        console.print(f"[bold red]✘ Erreur : {e}[/]")
        return None


def check_environment():
    """Vérifie l'environnement d'exécution."""
    console.print(Panel("[bold cyan]Vérification de l'environnement[/]", box=box.ROUNDED))

    # Python
    py_version = f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}"
    console.print(f"  [green]✔[/] Python {py_version}")

    # Rich
    try:
        import rich
        console.print(f"  [green]✔[/] Rich {rich.__version__}")
    except ImportError:
        console.print("  [yellow]⚠[/] Rich non installé (installation automatique)")

    # Binaire
    binary = find_binary()
    if binary:
        console.print(f"  [green]✔[/] Binaire : {binary}")
    else:
        console.print("  [red]✘[/] Binaire labosurf introuvable")
        console.print("    Compilez : [cyan]go build -o labosurf .[/]")

    # Store
    if Path(STORE_FILE).exists():
        console.print(f"  [green]✔[/] Store : {STORE_FILE}")
    else:
        console.print(f"  [yellow]⚠[/] Store {STORE_FILE} absent (sera créé)")

    # Licence
    if Path(LICENSE_FILE).exists():
        console.print(f"  [green]✔[/] Licence : {LICENSE_FILE}")
    else:
        console.print(f"  [yellow]⚠[/] Licence {LICENSE_FILE} absente")


def cmd_create(args):
    """Crée un nouveau compte client."""
    id_val = args.get("id", "")
    if not id_val:
        id_val = Prompt.ask("Identifiant du compte")

    days = args.get("days", 0)
    if days == 0:
        days_str = Prompt.ask("Durée en jours (0 = illimité)", default="0")
        days = int(days_str)

    quota = args.get("quota", 0)
    if quota == 0:
        quota_str = Prompt.ask("Quota en octets (0 = illimité)", default="0")
        quota = int(quota_str)

    password = args.get("password", "")

    cmd_args = ["create", "-id", id_val]
    if days > 0:
        cmd_args += ["-days", str(days)]
    if quota > 0:
        cmd_args += ["-quota", str(quota)]
    if password:
        cmd_args += ["-password", password]

    with Progress(SpinnerColumn(), TextColumn("[progress.description]{task.description}")) as progress:
        task = progress.add_task("Création du compte...", total=None)
        result = run_admin(cmd_args, STORE_FILE)
        progress.update(task, completed=True)

    if result and result.returncode == 0:
        console.print(Panel(
            result.stdout,
            title="[bold green]Compte créé[/]",
            border_style="green",
            box=box.ROUNDED
        ))
    else:
        err = result.stderr if result else "Binaire introuvable"
        console.print(Panel(
            f"[red]{err}[/]",
            title="[bold red]Erreur[/]",
            border_style="red",
            box=box.ROUNDED
        ))


def cmd_list():
    """Liste tous les comptes."""
    with Progress(SpinnerColumn(), TextColumn("[progress.description]{task.description}")) as progress:
        task = progress.add_task("Chargement des comptes...", total=None)
        result = run_admin(["list"], STORE_FILE)
        progress.update(task, completed=True)

    if result and result.returncode == 0:
        lines = result.stdout.strip().split("\n")
        if not lines or lines[0].startswith("(aucun)"):
            console.print("[yellow]Aucun compte trouvé.[/]")
            return

        table = Table(title="Comptes clients", box=box.ROUNDED, show_lines=True)
        table.add_column("ID", style="cyan", no_wrap=True)
        table.add_column("État", style="green")
        table.add_column("Expiration", style="yellow")
        table.add_column("Quota", style="blue")
        table.add_column("Conn", justify="right")
        table.add_column("IPs", justify="right")

        # Skip header line
        for line in lines[1:]:
            if line.startswith("---"):
                continue
            parts = line.split()
            if len(parts) >= 6:
                table.add_row(*parts[:6])

        console.print(table)
    else:
        err = result.stderr if result else "Binaire introuvable"
        console.print(f"[red]✘ {err}[/]")


def cmd_show(args):
    """Affiche les détails d'un compte."""
    id_val = args.get("id", "")
    if not id_val:
        id_val = Prompt.ask("Identifiant du compte")

    result = run_admin(["show", "-id", id_val], STORE_FILE)

    if result and result.returncode == 0:
        console.print(Panel(
            result.stdout,
            title=f"[bold cyan]Compte {id_val}[/]",
            border_style="cyan",
            box=box.ROUNDED
        ))
    else:
        err = result.stderr if result else "Binaire introuvable"
        console.print(f"[red]✘ {err}[/]")


def cmd_enable(args):
    """Active un compte."""
    id_val = args.get("id", "")
    if not id_val:
        id_val = Prompt.ask("Identifiant du compte")

    result = run_admin(["enable", "-id", id_val], STORE_FILE)
    if result and result.returncode == 0:
        console.print(f"[green]✔ Compte '{id_val}' activé.[/]")
    else:
        err = result.stderr if result else "Erreur"
        console.print(f"[red]✘ {err}[/]")


def cmd_disable(args):
    """Désactive un compte."""
    id_val = args.get("id", "")
    if not id_val:
        id_val = Prompt.ask("Identifiant du compte")

    result = run_admin(["disable", "-id", id_val], STORE_FILE)
    if result and result.returncode == 0:
        console.print(f"[green]✔ Compte '{id_val}' désactivé.[/]")
    else:
        err = result.stderr if result else "Erreur"
        console.print(f"[red]✘ {err}[/]")


def cmd_delete(args):
    """Supprime un compte."""
    id_val = args.get("id", "")
    if not id_val:
        id_val = Prompt.ask("Identifiant du compte")

    if not Confirm.ask(f"[bold red]Supprimer le compte '{id_val}' ?[/]"):
        console.print("[yellow]Annulé.[/]")
        return

    result = run_admin(["delete", "-id", id_val], STORE_FILE)
    if result and result.returncode == 0:
        console.print(f"[green]✔ Compte '{id_val}' supprimé.[/]")
    else:
        err = result.stderr if result else "Erreur"
        console.print(f"[red]✘ {err}[/]")


def cmd_link(args):
    """Affiche le lien client."""
    id_val = args.get("id", "")
    if not id_val:
        id_val = Prompt.ask("Identifiant du compte")

    base = args.get("base", "")
    cmd_args = ["link", "-id", id_val]
    if base:
        cmd_args += ["-base", base]

    result = run_admin(cmd_args, STORE_FILE)
    if result and result.returncode == 0:
        link = result.stdout.strip()
        console.print(Panel(
            f"[cyan]{link}[/]",
            title="[bold green]Lien client[/]",
            border_style="green",
            box=box.ROUNDED
        ))
    else:
        err = result.stderr if result else "Erreur"
        console.print(f"[red]✘ {err}[/]")


def cmd_token_new(args):
    """Régénère le lien client."""
    id_val = args.get("id", "")
    if not id_val:
        id_val = Prompt.ask("Identifiant du compte")

    base = args.get("base", "")
    cmd_args = ["token-new", "-id", id_val]
    if base:
        cmd_args += ["-base", base]

    result = run_admin(cmd_args, STORE_FILE)
    if result and result.returncode == 0:
        console.print(Panel(
            result.stdout,
            title="[bold green]Lien client régénéré[/]",
            border_style="green",
            box=box.ROUNDED
        ))
    else:
        err = result.stderr if result else "Erreur"
        console.print(f"[red]✘ {err}[/]")


def cmd_license_verify():
    """Vérifie la licence."""
    result = run_admin(["license-verify", "-file", LICENSE_FILE])
    if result and result.returncode == 0:
        status_color = "green" if "valide" in result.stdout.lower() else "red"
        console.print(Panel(
            result.stdout,
            title="[bold]Licence[/]",
            border_style=status_color,
            box=box.ROUNDED
        ))
    else:
        err = result.stderr if result else "Aucune licence trouvée"
        console.print(f"[yellow]⚠ {err}[/]")


def cmd_license_create(args):
    """Crée une nouvelle licence."""
    id_val = args.get("id", "")
    if not id_val:
        id_val = Prompt.ask("Identifiant de la licence")
    days = args.get("days", 365)
    max_users = args.get("max_users", 10)

    cmd_args = ["license-create", "-id", id_val, "-days", str(days), "-max-users", str(max_users), "-out", LICENSE_FILE]
    result = run_admin(cmd_args)
    if result and result.returncode == 0:
        console.print(Panel(
            result.stdout,
            title="[bold green]Licence créée[/]",
            border_style="green",
            box=box.ROUNDED
        ))
    else:
        err = result.stderr if result else "Erreur"
        console.print(f"[red]✘ {err}[/]")


def show_banner():
    """Affiche la bannière du logiciel."""
    banner_text = Text()
    banner_text.append("╔══════════════════════════════════════════════════╗\n", style="bold green")
    banner_text.append("║           LABOSURF PRO — CLI Admin              ║\n", style="bold green")
    banner_text.append("║         Gestion des comptes & licences          ║\n", style="cyan")
    banner_text.append("╚══════════════════════════════════════════════════╝\n", style="bold green")
    console.print(banner_text)


def show_menu():
    """Affiche le menu principal."""
    table = Table(box=box.ROUNDED, show_header=False, title="Menu principal")
    table.add_column("N°", style="bold cyan", width=4)
    table.add_column("Commande", style="white")
    table.add_column("Description", style="dim")

    table.add_row("1", "create", "Créer un compte")
    table.add_row("2", "list", "Lister les comptes")
    table.add_row("3", "show", "Afficher un compte")
    table.add_row("4", "enable", "Activer un compte")
    table.add_row("5", "disable", "Désactiver un compte")
    table.add_row("6", "delete", "Supprimer un compte")
    table.add_row("7", "link", "Afficher le lien client")
    table.add_row("8", "token-new", "Régénérer le lien client")
    table.add_row("─", "─", "─")
    table.add_row("L", "license-verify", "Vérifier la licence")
    table.add_row("C", "license-create", "Créer une licence")
    table.add_row("─", "─", "─")
    table.add_row("0", "Quitter", "")

    console.print(table)


def interactive_mode():
    """Mode interactif principal."""
    show_banner()
    check_environment()
    console.print()

    while True:
        show_menu()
        choice = Prompt.ask("\n[bold cyan]LABOSURF ►[/]", default="0")

        if choice == "1":
            cmd_create({})
        elif choice == "2":
            cmd_list()
        elif choice == "3":
            cmd_show({})
        elif choice == "4":
            cmd_enable({})
        elif choice == "5":
            cmd_disable({})
        elif choice == "6":
            cmd_delete({})
        elif choice == "7":
            cmd_link({})
        elif choice == "8":
            cmd_token_new({})
        elif choice.upper() == "L":
            cmd_license_verify()
        elif choice.upper() == "C":
            cmd_license_create({})
        elif choice == "0":
            console.print("[dim]Au revoir.[/]")
            break
        else:
            console.print("[yellow]Option inconnue.[/]")

        console.print()


def parse_args():
    """Parse les arguments en ligne de commande."""
    import argparse
    parser = argparse.ArgumentParser(description="LABOSURF PRO — CLI Admin")
    parser.add_argument("command", nargs="?", choices=["create", "list", "show", "enable", "disable", "delete", "link", "token-new", "license-verify", "license-create", "check"], help="Commande à exécuter")
    parser.add_argument("--id", help="Identifiant du compte")
    parser.add_argument("--days", type=int, default=0, help="Durée en jours")
    parser.add_argument("--quota", type=int, default=0, help="Quota en octets")
    parser.add_argument("--password", default="", help="Mot de passe")
    parser.add_argument("--base", default="", help="Base URL du portail")
    parser.add_argument("--store", default=None, help="Chemin du store")
    parser.add_argument("--max-users", type=int, default=10, help="Max utilisateurs (licence)")
    return parser.parse_args()


if __name__ == "__main__":
    if len(sys.argv) > 1:
        args = parse_args()
        if args.command == "check":
            check_environment()
        elif args.command == "create":
            cmd_create({"id": args.id, "days": args.days, "quota": args.quota, "password": args.password})
        elif args.command == "list":
            cmd_list()
        elif args.command == "show":
            cmd_show({"id": args.id})
        elif args.command == "enable":
            cmd_enable({"id": args.id})
        elif args.command == "disable":
            cmd_disable({"id": args.id})
        elif args.command == "delete":
            cmd_delete({"id": args.id})
        elif args.command == "link":
            cmd_link({"id": args.id, "base": args.base})
        elif args.command == "token-new":
            cmd_token_new({"id": args.id, "base": args.base})
        elif args.command == "license-verify":
            cmd_license_verify()
        elif args.command == "license-create":
            cmd_license_create({"id": args.id, "days": args.days, "max_users": args.max_users})
    else:
        interactive_mode()
