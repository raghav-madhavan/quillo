// Quillo: Admin › Addons. Install Stremio-style addons by pasting a manifest
// link, turn them on or off, and set the order they're asked in.
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { ArrowDown, ArrowUp, Link2, Puzzle, RefreshCw, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/page/PageHeader";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  type Addon,
  type AddonPreview,
  type AddonSummary,
  useAddonPresets,
  useAddons,
  useInstallAddon,
  usePreviewAddon,
  useRefreshAddon,
  useRemoveAddon,
  useReorderAddons,
  useSetAddonEnabled,
} from "@/quillo/addonsApi";

export default function AdminAddons() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Addons"
        subtitle="Addons add catalogs and streams to Quillo. Paste an addon's manifest link to install it. Everyone on this server gets the addons installed here, in the order shown."
      />
      <AddAddonCard />
      <PresetsCard />
      <InstalledCard />
    </div>
  );
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Something went wrong. Try again.";
}

function AddAddonCard() {
  const [url, setUrl] = useState("");
  const preview = usePreviewAddon();
  const install = useInstallAddon();
  const checked = preview.data && preview.variables === url ? preview.data : undefined;

  function check(e: FormEvent) {
    e.preventDefault();
    if (!url.trim()) return;
    install.reset();
    preview.mutate(url);
  }

  function installChecked() {
    install.mutate(url, {
      onSuccess: (addon) => {
        toast.success(`${addon.name} installed.`);
        setUrl("");
        preview.reset();
      },
    });
  }

  const error = install.error ?? (preview.variables === url ? preview.error : null);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Add an addon</CardTitle>
        <CardDescription>
          Works with any Stremio addon, including an AIOStreams manifest link. If the addon has a
          configure page, finish setting it up there first and paste the link it gives you.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <form onSubmit={check} className="flex flex-col gap-2 sm:flex-row">
          <Label htmlFor="addon-url" className="sr-only">
            Manifest link
          </Label>
          <Input
            id="addon-url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://…/manifest.json"
            autoComplete="off"
            spellCheck={false}
            inputMode="url"
            className="flex-1"
          />
          <Button type="submit" variant="outline" disabled={!url.trim() || preview.isPending}>
            <Link2 />
            {preview.isPending ? "Checking…" : "Check link"}
          </Button>
        </form>

        {error ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(error)}
          </p>
        ) : null}

        {checked ? (
          <PreviewPanel
            preview={checked}
            installing={install.isPending}
            onInstall={installChecked}
          />
        ) : null}
      </CardContent>
    </Card>
  );
}

function PreviewPanel({
  preview,
  installing,
  onInstall,
}: {
  preview: AddonPreview;
  installing: boolean;
  onInstall: () => void;
}) {
  return (
    <div className="bg-muted/40 flex flex-col gap-4 rounded-lg border p-4 sm:flex-row sm:items-center">
      <AddonLogo name={preview.name} logo={preview.logo} />
      <div className="min-w-0 flex-1 space-y-1">
        <p className="font-medium">
          {preview.name} <span className="text-muted-foreground text-sm">v{preview.version}</span>
        </p>
        {preview.description ? (
          <p className="text-muted-foreground line-clamp-2 text-sm">{preview.description}</p>
        ) : null}
        <SummaryBadges summary={preview.summary} />
      </div>
      <Button onClick={onInstall} disabled={installing}>
        {installing ? "Installing…" : "Install"}
      </Button>
    </div>
  );
}

function PresetsCard() {
  const presets = useAddonPresets();
  const install = useInstallAddon();
  const available = presets.data?.filter((p) => !p.installed) ?? [];
  if (available.length === 0) return null;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Recommended</CardTitle>
        <CardDescription>
          Catalog addons that fill the home screen. Quillo doesn't include stream sources; add those
          yourself.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {available.map((p) => (
          <div key={p.key} className="flex items-center gap-4">
            <AddonLogo name={p.name} />
            <div className="min-w-0 flex-1">
              <p className="font-medium">{p.name}</p>
              <p className="text-muted-foreground text-sm">{p.description}</p>
            </div>
            <Button
              variant="outline"
              size="sm"
              disabled={install.isPending}
              onClick={() =>
                install.mutate(p.manifest_url, {
                  onSuccess: (a) => toast.success(`${a.name} installed.`),
                  onError: (e) => toast.error(errorMessage(e)),
                })
              }
            >
              Add
            </Button>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function InstalledCard() {
  const addons = useAddons();
  const reorder = useReorderAddons();
  const items = addons.data ?? [];

  function move(index: number, by: -1 | 1) {
    const ids = items.map((a) => a.id);
    const target = index + by;
    if (target < 0 || target >= ids.length) return;
    const [moved] = ids.splice(index, 1);
    if (moved === undefined) return;
    ids.splice(target, 0, moved);
    reorder.mutate(ids, { onError: (e) => toast.error(errorMessage(e)) });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Installed</CardTitle>
        <CardDescription>
          Addons higher in the list are asked first: their catalogs appear first on the home screen,
          and their streams are preferred.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {addons.isPending ? (
          <div className="space-y-3">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : addons.isError ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(addons.error)}
          </p>
        ) : items.length === 0 ? (
          <div className="text-muted-foreground flex flex-col items-center gap-2 py-8 text-center text-sm">
            <Puzzle className="size-6" />
            No addons yet. Paste a manifest link above to add one.
          </div>
        ) : (
          <ul className="divide-y">
            {items.map((addon, i) => (
              <AddonRow
                key={addon.id}
                addon={addon}
                first={i === 0}
                last={i === items.length - 1}
                moving={reorder.isPending}
                onMove={(by) => move(i, by)}
              />
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function AddonRow({
  addon,
  first,
  last,
  moving,
  onMove,
}: {
  addon: Addon;
  first: boolean;
  last: boolean;
  moving: boolean;
  onMove: (by: -1 | 1) => void;
}) {
  const setEnabled = useSetAddonEnabled();
  const refresh = useRefreshAddon();
  const remove = useRemoveAddon();
  const [confirmRemove, setConfirmRemove] = useState(false);

  return (
    <li className="flex flex-col gap-3 py-4 first:pt-0 last:pb-0 sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 items-start gap-4">
        <AddonLogo name={addon.name} logo={addon.logo} />
        <div className="min-w-0 flex-1 space-y-1">
          <p className="font-medium">
            {addon.name}{" "}
            <span className="text-muted-foreground text-sm">
              v{addon.version} · {addon.manifest_host}
            </span>
          </p>
          <SummaryBadges summary={addon.summary} />
          {addon.last_error ? <p className="text-destructive text-sm">{addon.last_error}</p> : null}
        </div>
      </div>
      <div className="flex items-center gap-1 self-end sm:self-auto">
        <Button
          variant="ghost"
          size="icon"
          aria-label={`Move ${addon.name} up`}
          disabled={first || moving}
          onClick={() => onMove(-1)}
        >
          <ArrowUp />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          aria-label={`Move ${addon.name} down`}
          disabled={last || moving}
          onClick={() => onMove(1)}
        >
          <ArrowDown />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          aria-label={`Check ${addon.name} for updates`}
          disabled={refresh.isPending}
          onClick={() =>
            refresh.mutate(addon.id, {
              onSuccess: (a) =>
                a.last_error
                  ? toast.error(a.last_error)
                  : toast.success(`${a.name} is up to date (v${a.version}).`),
              onError: (e) => toast.error(errorMessage(e)),
            })
          }
        >
          <RefreshCw className={refresh.isPending ? "animate-spin" : undefined} />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          aria-label={`Remove ${addon.name}`}
          onClick={() => setConfirmRemove(true)}
        >
          <Trash2 />
        </Button>
        <Switch
          className="ml-2"
          checked={addon.enabled}
          disabled={setEnabled.isPending}
          aria-label={addon.enabled ? `Turn off ${addon.name}` : `Turn on ${addon.name}`}
          onCheckedChange={(enabled) =>
            setEnabled.mutate(
              { id: addon.id, enabled },
              { onError: (e) => toast.error(errorMessage(e)) },
            )
          }
        />
      </div>

      <AlertDialog open={confirmRemove} onOpenChange={setConfirmRemove}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {addon.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Its catalogs and streams disappear for everyone on this server. You can add it again
              later with its manifest link.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                remove.mutate(addon.id, {
                  onSuccess: () => toast.success(`${addon.name} removed.`),
                  onError: (e) => toast.error(errorMessage(e)),
                })
              }
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </li>
  );
}

function SummaryBadges({ summary }: { summary: AddonSummary }) {
  const badges: string[] = [];
  if (summary.home_row_catalogs > 0)
    badges.push(
      `${summary.home_row_catalogs} home ${summary.home_row_catalogs === 1 ? "row" : "rows"}`,
    );
  if (summary.search) badges.push("Search");
  if (summary.metadata) badges.push("Details");
  if (summary.streams) badges.push("Streams");
  return (
    <div className="flex flex-wrap gap-1.5">
      {badges.map((b) => (
        <Badge key={b} variant="secondary">
          {b}
        </Badge>
      ))}
      {summary.types.map((t) => (
        <Badge key={t} variant="outline" className="text-muted-foreground capitalize">
          {t}
        </Badge>
      ))}
    </div>
  );
}

function AddonLogo({ name, logo }: { name: string; logo?: string }) {
  const [broken, setBroken] = useState(false);
  if (logo && !broken) {
    return (
      <img
        src={logo}
        alt=""
        referrerPolicy="no-referrer"
        onError={() => setBroken(true)}
        className="bg-muted size-10 shrink-0 rounded-lg object-contain"
      />
    );
  }
  return (
    <div className="bg-muted text-muted-foreground flex size-10 shrink-0 items-center justify-center rounded-lg font-semibold">
      {name.slice(0, 1).toUpperCase()}
    </div>
  );
}
