import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Config, RdioScannerAdminService } from '../admin.service';

interface RRCountry { id: number; name: string; }
interface RRState { id: number; name: string; code: string; }
interface RRCounty { id: number; name: string; }
interface RRAgency { id: number; name: string; type: string; }
interface RRSystem { id: number; name: string; type: string; flavor: string; }
interface RRTalkgroup {
    decimal_id: number;
    alpha_tag: string;
    description: string;
    mode: string;
    tag: string;
    category: string;
    encrypted: number;
    frequency: number;
    last_updated: string;
    selected?: boolean;
    visible?: boolean;
}

// 3-level tree: Level1 > Level2 > leaf talkgroups
interface TreeNode {
    name: string;
    expanded: boolean;
    children: TreeNode[];
    talkgroups: RRTalkgroup[];  // only populated at leaf level (level 2)
}

interface RRUpdate {
    frequency: number;
    tg_id: number;
    alpha_tag: string;
    description: string;
    tone: string;
    service_tag: string;
    last_updated: string;
}

const SERVICE_TAGS = [
    'Aircraft', 'Business', 'Corrections', 'Emergency Ops', 'EMS Dispatch',
    'EMS-Tac', 'EMS-Talk', 'Federal', 'Fire Dispatch', 'Fire-Tac',
    'Fire-Talk', 'Ham', 'Hospital', 'Interop', 'Law Dispatch', 'Law Tac',
    'Law Talk', 'Media', 'Military', 'Multi-Dispatch', 'Multi-Tac',
    'Multi-Talk', 'Other', 'Public Works', 'Railroad', 'Schools',
    'Security', 'Transportation', 'Utilities', 'Data', 'Deprecated',
];

@Component({
    selector: 'rdio-scanner-admin-rr-import',
    templateUrl: './rr-import.component.html',
    styleUrls: ['./rr-import.component.scss'],
})
export class RdioScannerAdminRRImportComponent implements OnInit {
    @Output() config = new EventEmitter<Config>();

    // API key gate
    hasApiKey = true;  // assume true until checked
    apiKeyChecked = false;
    connectError = '';

    // Connection
    username = '';
    password = '';
    connected = false;
    connecting = false;
    saveCredentials = false;
    hasSavedCreds = false;
    passwordSentinel = '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022';
    userEditedPassword = false;

    // Location drill-down
    countries: RRCountry[] = [];
    states: RRState[] = [];
    counties: RRCounty[] = [];
    agencies: RRAgency[] = [];
    systems: RRSystem[] = [];

    selectedCountry: number | null = null;
    selectedState: number | null = null;
    selectedCounty: number | null = null;
    selectedAgency: number | null = null;
    selectedSystem: number | null = null;

    // Data mode
    dataMode: 'none' | 'conventional' | 'agencies' | 'trunksystems' | 'nationwide' = 'none';

    // Talkgroups + tree
    allTalkgroups: RRTalkgroup[] = [];
    tree: TreeNode[] = [];
    loading = false;

    // Inner tabs
    innerTab = 0;

    // Tag Options
    tagRemoveSpaces = true;
    tagNoModifySmall = true;
    tagUseShortAlpha = true;
    tagUpperCase = false;

    // Misc
    miscNoEncrypted = true;
    miscLockEncrypted = true;
    miscNoTelemetry = false;
    miscAutoExpand = false;

    // Service Tags
    serviceTagsEnabled = false;
    serviceTags: { name: string; enabled: boolean; count: number }[] = [];

    // TG Filters
    filterAnalog = true;
    filterDigital = true;
    filterMixed = true;
    filterTDMA = true;
    filterEncrypted = true;
    filterPartialEncrypted = true;

    // Updates
    updates: RRUpdate[] = [];

    constructor(
        private http: HttpClient,
        private adminService: RdioScannerAdminService,
    ) {}

    ngOnInit(): void {
        this.checkApiKey();
        this.loadSavedCredsAndAutoConnect();
    }

    checkApiKey(): void {
        this.http.get<any>(this.getUrl('/status'), { headers: this.getHeaders() }).subscribe({
            next: (data) => {
                this.hasApiKey = data.has_api_key || false;
                this.apiKeyChecked = true;
            },
            error: () => {
                this.hasApiKey = false;
                this.apiKeyChecked = true;
            },
        });
    }

    loadSavedCredsAndAutoConnect(): void {
        this.http.get<any>(this.getUrl('/saved-creds'), { headers: this.getHeaders() }).subscribe({
            next: (data) => {
                if (data.has_saved) {
                    this.username = data.username || '';
                    this.password = this.passwordSentinel;
                    this.userEditedPassword = false;
                    this.hasSavedCreds = true;
                    this.saveCredentials = true;
                    // Auto-connect silently
                    this.autoConnect();
                }
            },
            error: () => {},
        });
    }

    private autoConnect(): void {
        this.http.post<any>(this.getUrl('/connect'),
            { username: this.username, password: this.password, save: true },
            { headers: this.getHeaders() },
        ).subscribe({
            next: () => {
                this.connected = true;
                this.loadCountries();
            },
            error: () => {
                // Silent failure — user can click Connect manually
            },
        });
    }

    onPasswordInput(): void {
        this.userEditedPassword = true;
    }

    clearSavedCreds(): void {
        this.http.post<any>(this.getUrl('/clear-creds'), {}, { headers: this.getHeaders() }).subscribe({
            next: () => {
                this.username = '';
                this.password = '';
                this.userEditedPassword = false;
                this.hasSavedCreds = false;
                this.saveCredentials = false;
                this.connected = false;
            },
            error: () => {},
        });
    }

    private getUrl(path: string): string {
        return `${window.location.href}/../api/admin/rr${path}`;
    }

    private getHeaders(): HttpHeaders {
        return new HttpHeaders({
            Authorization: (this.adminService as any).token || '',
        });
    }

    // ── Connection ──

    connect(): void {
        this.connecting = true;
        this.connectError = '';
        this.http.post<any>(this.getUrl('/connect'),
            { username: this.username, password: this.password, save: this.saveCredentials },
            { headers: this.getHeaders() },
        ).subscribe({
            next: () => {
                this.connected = true;
                this.connecting = false;
                this.hasSavedCreds = this.saveCredentials;
                if (!this.userEditedPassword) {
                    this.password = this.passwordSentinel;
                }
                this.loadCountries();
            },
            error: (err) => {
                if (err.status === 401) {
                    const detail = err.error?.detail || err.error;
                    this.connectError = typeof detail === 'string' && detail.includes('Username')
                        ? detail
                        : 'Invalid RadioReference credentials. Check your username and password.';
                } else {
                    this.connectError = `Error ${err.status}: ${typeof err.error === 'string' ? err.error : JSON.stringify(err.error)}`;
                }
                this.connecting = false;
            },
        });
    }

    // ── Location drill-down ──

    loadCountries(): void {
        this.http.get<RRCountry[]>(this.getUrl('/countries'), { headers: this.getHeaders() }).subscribe({
            next: (data) => {
                this.countries = data || [];
                const usa = this.countries.find(c => c.name === 'United States');
                if (usa) {
                    this.selectedCountry = usa.id;
                    this.onCountryChange();
                }
            },
        });
    }

    onCountryChange(): void {
        this.states = [];
        this.counties = [];
        this.agencies = [];
        this.systems = [];
        this.selectedState = null;
        this.selectedCounty = null;
        this.selectedAgency = null;
        this.selectedSystem = null;
        this.clearData();
        this.dataMode = 'none';
        if (this.selectedCountry) {
            this.http.get<RRState[]>(this.getUrl(`/states/${this.selectedCountry}`), { headers: this.getHeaders() }).subscribe({
                next: (data) => { this.states = data || []; },
            });
        }
    }

    onStateChange(): void {
        this.counties = [];
        this.agencies = [];
        this.systems = [];
        this.selectedCounty = null;
        this.selectedAgency = null;
        this.selectedSystem = null;
        this.clearData();
        this.dataMode = 'none';
        if (this.selectedState) {
            this.http.get<RRCounty[]>(this.getUrl(`/counties/${this.selectedState}`), { headers: this.getHeaders() }).subscribe({
                next: (data) => { this.counties = data || []; },
            });
        }
    }

    onCountyChange(): void {
        this.agencies = [];
        this.systems = [];
        this.clearData();
        this.dataMode = 'none';
        this.selectedAgency = null;
        this.selectedSystem = null;
        if (this.selectedCounty) {
            this.http.get<RRAgency[]>(this.getUrl(`/agencies/${this.selectedCounty}`), { headers: this.getHeaders() }).subscribe({
                next: (data) => { this.agencies = data || []; },
            });
            this.http.get<RRSystem[]>(this.getUrl(`/systems/${this.selectedCounty}`), { headers: this.getHeaders() }).subscribe({
                next: (data) => {
                    this.systems = data || [];
                    if (this.systems.length === 1) {
                        this.dataMode = 'trunksystems';
                        this.selectedSystem = this.systems[0].id;
                        this.loadTalkgroups();
                    }
                },
            });
            this.http.get<RRUpdate[]>(this.getUrl(`/updates/${this.selectedCounty}`), { headers: this.getHeaders() }).subscribe({
                next: (data) => { this.updates = data || []; },
            });
        }
    }

    // ── Mode selection ──

    selectConventional(): void {
        if (!this.selectedCounty) return;
        this.dataMode = 'conventional';
        this.clearData();
        this.loading = true;
        this.http.get<RRTalkgroup[]>(this.getUrl(`/frequencies/${this.selectedCounty}`), { headers: this.getHeaders() }).subscribe({
            next: (data) => this.loadData(data),
            error: () => { this.loading = false; },
        });
    }

    selectAgenciesMode(): void {
        this.dataMode = 'agencies';
        this.clearData();
        if (this.selectedAgency) {
            this.loadAgencyFreqs();
        }
    }

    onAgencyChange(): void {
        this.clearData();
        if (this.selectedAgency) {
            this.loadAgencyFreqs();
        }
    }

    private loadAgencyFreqs(): void {
        if (!this.selectedAgency) return;
        this.loading = true;
        this.http.get<RRTalkgroup[]>(this.getUrl(`/agency-freqs/${this.selectedAgency}`), { headers: this.getHeaders() }).subscribe({
            next: (data) => this.loadData(data),
            error: () => { this.loading = false; },
        });
    }

    selectTrunkSystemsMode(): void {
        this.dataMode = 'trunksystems';
        this.clearData();
        if (this.selectedSystem) {
            this.loadTalkgroups();
        }
    }

    onSystemChange(): void {
        this.clearData();
        if (this.selectedSystem) {
            this.loadTalkgroups();
        }
    }

    private loadTalkgroups(): void {
        if (!this.selectedSystem) return;
        this.loading = true;
        this.http.get<RRTalkgroup[]>(this.getUrl(`/talkgroups/${this.selectedSystem}`), { headers: this.getHeaders() }).subscribe({
            next: (data) => this.loadData(data),
            error: () => { this.loading = false; },
        });
    }

    selectNationwide(): void {
        this.dataMode = 'nationwide';
        this.clearData();
        if (this.selectedCounty) {
            this.loading = true;
            this.http.get<RRTalkgroup[]>(this.getUrl(`/frequencies/${this.selectedCounty}`), { headers: this.getHeaders() }).subscribe({
                next: (data) => this.loadData(data),
                error: () => { this.loading = false; },
            });
        }
    }

    // ── Data + tree building ──

    private clearData(): void {
        this.allTalkgroups = [];
        this.tree = [];
    }

    private loadData(data: RRTalkgroup[]): void {
        this.allTalkgroups = (data || []).map(tg => ({ ...tg, selected: true, visible: true }));
        this.buildTree();
        this.buildServiceTags();
        this.loading = false;
    }

    /**
     * Build 3-level tree from flat talkgroup list.
     * category field format: "Level1 / Level2" or just "Level1"
     * Trunk systems with no "/" get a single child matching the parent name.
     */
    buildTree(): void {
        const l1Map = new Map<string, Map<string, RRTalkgroup[]>>();

        for (const tg of this.allTalkgroups) {
            if (!tg.visible) continue;
            const cat = tg.category || 'Uncategorized';
            const parts = cat.split(' / ');
            const l1 = parts[0] || 'Uncategorized';
            const l2 = parts.length > 1 ? parts.slice(1).join(' / ') : 'General';

            if (!l1Map.has(l1)) l1Map.set(l1, new Map());
            const l2Map = l1Map.get(l1)!;
            if (!l2Map.has(l2)) l2Map.set(l2, []);
            l2Map.get(l2)!.push(tg);
        }

        // Preserve expanded state
        const prevExpanded = new Map<string, boolean>();
        const prevL2Expanded = new Map<string, boolean>();
        for (const l1 of this.tree) {
            prevExpanded.set(l1.name, l1.expanded);
            for (const l2 of l1.children) {
                prevL2Expanded.set(`${l1.name}/${l2.name}`, l2.expanded);
            }
        }

        this.tree = Array.from(l1Map.entries()).map(([l1Name, l2Map]) => ({
            name: l1Name,
            expanded: prevExpanded.get(l1Name) ?? false,
            talkgroups: [],
            children: Array.from(l2Map.entries()).map(([l2Name, tgs]) => ({
                name: l2Name,
                expanded: prevL2Expanded.get(`${l1Name}/${l2Name}`) ?? false,
                children: [],
                talkgroups: tgs,
            })),
        }));
    }

    // ── Tree checkbox helpers (reusable at any level) ──

    getLeaves(node: TreeNode): RRTalkgroup[] {
        if (node.talkgroups.length > 0) return node.talkgroups;
        return node.children.flatMap(c => this.getLeaves(c));
    }

    isNodeAllSelected(node: TreeNode): boolean {
        return this.getLeaves(node).every(tg => tg.selected);
    }

    isNodePartial(node: TreeNode): boolean {
        const leaves = this.getLeaves(node);
        const sel = leaves.filter(tg => tg.selected).length;
        return sel > 0 && sel < leaves.length;
    }

    toggleNode(node: TreeNode, checked: boolean): void {
        this.getLeaves(node).forEach(tg => tg.selected = checked);
    }

    nodeLeafCount(node: TreeNode): number {
        return this.getLeaves(node).length;
    }

    // ── Global selection ──

    get selectedCount(): number {
        return this.allTalkgroups.filter(tg => tg.visible && tg.selected).length;
    }

    get totalCount(): number {
        return this.allTalkgroups.filter(tg => tg.visible).length;
    }

    selectAll(): void {
        this.allTalkgroups.filter(tg => tg.visible).forEach(tg => tg.selected = true);
    }

    unselectAll(): void {
        this.allTalkgroups.forEach(tg => tg.selected = false);
    }

    expandAll(): void {
        for (const l1 of this.tree) {
            l1.expanded = true;
            for (const l2 of l1.children) {
                l2.expanded = true;
            }
        }
    }

    collapseAll(): void {
        for (const l1 of this.tree) {
            l1.expanded = false;
            for (const l2 of l1.children) {
                l2.expanded = false;
            }
        }
    }

    // ── Service Tags ──

    private getTgTags(tg: RRTalkgroup): string[] {
        if (!tg.tag) return ['Other'];
        return tg.tag.split(',').map(t => t.trim()).filter(t => t.length > 0);
    }

    buildServiceTags(): void {
        const counts = new Map<string, number>();
        for (const tg of this.allTalkgroups) {
            for (const tag of this.getTgTags(tg)) {
                counts.set(tag, (counts.get(tag) || 0) + 1);
            }
        }
        this.serviceTags = SERVICE_TAGS.map(name => ({
            name, enabled: true, count: counts.get(name) || 0,
        }));
    }

    applyServiceTagFilter(): void {
        const enabled = new Set(this.serviceTags.filter(st => st.enabled).map(st => st.name));
        this.allTalkgroups.forEach(tg => {
            const tgTags = this.getTgTags(tg);
            const matches = tgTags.some(t => enabled.has(t));
            tg.visible = matches;
            if (!matches) tg.selected = false;
        });
        this.buildTree();
        this.innerTab = 0;
    }

    serviceTagAllOn(): void { this.serviceTags.forEach(st => st.enabled = true); }
    serviceTagAllOff(): void { this.serviceTags.forEach(st => st.enabled = false); }

    // ── TG Filters ──

    applyTGFilter(): void {
        this.allTalkgroups.forEach(tg => {
            const mode = (tg.mode || '').toLowerCase();
            const enc = tg.encrypted || 0;
            let show = false;
            if (this.filterAnalog && mode.includes('a') && !mode.includes('d')) show = true;
            if (this.filterDigital && mode.includes('d') && !mode.includes('a')) show = true;
            if (this.filterMixed && mode.includes('a') && mode.includes('d')) show = true;
            if (this.filterTDMA && mode.includes('t')) show = true;
            if (this.filterEncrypted && enc === 1) show = true;
            if (this.filterPartialEncrypted && enc === 2) show = true;
            if (enc === 0 && !mode.includes('t')) show = true;
            tg.visible = show;
            if (!show) tg.selected = false;
        });
        this.buildTree();
        this.innerTab = 0;
    }

    tgFilterAllOn(): void {
        this.filterAnalog = true;
        this.filterDigital = true;
        this.filterMixed = true;
        this.filterTDMA = true;
        this.filterEncrypted = true;
        this.filterPartialEncrypted = true;
    }

    // ── Import — identical to CSV import pattern ──

    async import(): Promise<void> {
        let selected = this.allTalkgroups.filter(tg => tg.visible && tg.selected);

        if (this.miscNoEncrypted) {
            selected = selected.filter(tg => !tg.encrypted);
        }
        if (this.miscNoTelemetry) {
            selected = selected.filter(tg =>
                !(tg.tag || '').toLowerCase().includes('data') &&
                !(tg.description || '').toLowerCase().includes('telemetry')
            );
        }

        selected = selected.map(tg => {
            let label = tg.alpha_tag;
            if (this.tagRemoveSpaces && label.length >= 16) {
                label = label.replace(/\s+/g, '');
            }
            if (this.tagUpperCase) {
                label = label.toUpperCase();
            }
            return { ...tg, alpha_tag: label };
        });

        if (selected.length === 0) return;

        // Get current config — same as CSV import
        const cfg = await this.adminService.getConfig();

        // Create groups and tags inline — same as CSV import
        selected.forEach((tg) => {
            const group = tg.category || 'Uncategorized';
            if (!cfg.groups?.find((g) => g.label === group)) {
                const id = cfg.groups?.reduce((pv, cv) => typeof cv._id === 'number' && cv._id >= pv ? cv._id + 1 : pv, 1);
                cfg.groups?.push({ _id: id, label: group });
            }
            const tag = tg.tag || 'Untagged';
            if (!cfg.tags?.find((t) => t.label === tag)) {
                const id = cfg.tags?.reduce((pv, cv) => typeof cv._id === 'number' && cv._id >= pv ? cv._id + 1 : pv, 1);
                cfg.tags?.push({ _id: id, label: tag });
            }
        });

        // Build talkgroups array — same shape as CSV import
        const talkgroups = selected.map((tg, idx) => {
            const groupId = cfg.groups?.find((g) => g.label === (tg.category || 'Uncategorized'))?._id;
            const tagId = cfg.tags?.find((t) => t.label === (tg.tag || 'Untagged'))?._id;
            return {
                id: tg.decimal_id,
                label: tg.alpha_tag,
                name: tg.description,
                order: idx + 1,
                tagId,
                groupId,
                frequency: tg.frequency || undefined,
            };
        });

        // Push new system with ONLY talkgroups — same as CSV import line 80:
        // config.systems?.unshift({ talkgroups });
        // No id, no label — user fills these in on the Config panel
        cfg.systems?.unshift({ talkgroups });

        // Reset
        this.allTalkgroups = [];
        this.tree = [];

        // Emit config — Config panel opens with new blank system at top
        this.config.emit(cfg);
    }
}
