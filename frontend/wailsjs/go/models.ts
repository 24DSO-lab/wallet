export namespace app {
	
	export class BalanceDTO {
	    asset: string;
	    network: string;
	    amount: string;
	    pendingAmount: string;
	    decimals: number;
	    updatedAt: string;
	    feeAsset?: string;
	    feeBalance?: string;
	
	    static createFrom(source: any = {}) {
	        return new BalanceDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.asset = source["asset"];
	        this.network = source["network"];
	        this.amount = source["amount"];
	        this.pendingAmount = source["pendingAmount"];
	        this.decimals = source["decimals"];
	        this.updatedAt = source["updatedAt"];
	        this.feeAsset = source["feeAsset"];
	        this.feeBalance = source["feeBalance"];
	    }
	}
	export class PrepareWalletDTO {
	    mnemonic: string;
	    challengePositions: number[];
	
	    static createFrom(source: any = {}) {
	        return new PrepareWalletDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mnemonic = source["mnemonic"];
	        this.challengePositions = source["challengePositions"];
	    }
	}
	export class QuoteDTO {
	    draftId: string;
	    asset: string;
	    network: string;
	    recipient: string;
	    amount: string;
	    fee: string;
	    feeAsset: string;
	    total: string;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new QuoteDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.draftId = source["draftId"];
	        this.asset = source["asset"];
	        this.network = source["network"];
	        this.recipient = source["recipient"];
	        this.amount = source["amount"];
	        this.fee = source["fee"];
	        this.feeAsset = source["feeAsset"];
	        this.total = source["total"];
	        this.warnings = source["warnings"];
	    }
	}
	export class SessionDTO {
	    locked: boolean;
	    profile: string;
	    timeout: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.locked = source["locked"];
	        this.profile = source["profile"];
	        this.timeout = source["timeout"];
	    }
	}
	export class TxDTO {
	    id: string;
	    asset: string;
	    direction: string;
	    amount: string;
	    fee: string;
	    feeAsset: string;
	    explorerUrl?: string;
	    status: string;
	    timestamp: string;
	    address: string;
	
	    static createFrom(source: any = {}) {
	        return new TxDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.asset = source["asset"];
	        this.direction = source["direction"];
	        this.amount = source["amount"];
	        this.fee = source["fee"];
	        this.feeAsset = source["feeAsset"];
	        this.explorerUrl = source["explorerUrl"];
	        this.status = source["status"];
	        this.timestamp = source["timestamp"];
	        this.address = source["address"];
	    }
	}
	export class WalletDTO {
	    id: string;
	    name: string;
	    balances: BalanceDTO[];
	
	    static createFrom(source: any = {}) {
	        return new WalletDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.balances = this.convertValues(source["balances"], BalanceDTO);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

