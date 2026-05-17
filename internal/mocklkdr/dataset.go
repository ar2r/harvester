package mocklkdr

import (
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/lkdr-api"
)

// Детерминированный набор данных: 2 бренда, 3 чека (r1—r3) за сентябрь 2026
// и фискальные детали к каждому чеку. Все данные синтетические.

var (
	brandGrocery = lkdr.Brand{Id: 1, Name: "Магазин у дома"}
	brandCoffee  = lkdr.Brand{Id: 2, Name: "Кофейня №7"}

	brands = []lkdr.Brand{brandGrocery, brandCoffee}

	r1Time = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	r2Time = time.Date(2026, 9, 5, 12, 30, 0, 0, time.UTC)
	r3Time = time.Date(2026, 9, 10, 18, 15, 0, 0, time.UTC)

	receipts = []lkdr.Receipt{
		{
			Key:                  "r1",
			BrandId:              pointer.To(brandGrocery.Id),
			BuyerType:            "INDIVIDUAL",
			CreatedDate:          lkdr.DateTime(r1Time),
			FiscalDocumentNumber: "0001",
			FiscalDriveNumber:    "drv-1",
			KktOwner:             "ООО «Магазин у дома»",
			KktOwnerInn:          "7700000001",
			ReceiveDate:          lkdr.DateTime(r1Time),
			TotalSum:             "1250.50",
		},
		{
			Key:                  "r2",
			BrandId:              pointer.To(brandCoffee.Id),
			BuyerType:            "INDIVIDUAL",
			CreatedDate:          lkdr.DateTime(r2Time),
			FiscalDocumentNumber: "0002",
			FiscalDriveNumber:    "drv-2",
			KktOwner:             "ООО «Кофейня №7»",
			KktOwnerInn:          "7700000002",
			ReceiveDate:          lkdr.DateTime(r2Time),
			TotalSum:             "480.00",
		},
		{
			Key:                  "r3",
			BrandId:              pointer.To(brandGrocery.Id),
			BuyerType:            "INDIVIDUAL",
			CreatedDate:          lkdr.DateTime(r3Time),
			FiscalDocumentNumber: "0003",
			FiscalDriveNumber:    "drv-3",
			KktOwner:             "ООО «Магазин у дома»",
			KktOwnerInn:          "7700000001",
			ReceiveDate:          lkdr.DateTime(r3Time),
			TotalSum:             "2390.10",
		},
	}

	fiscalData = map[string]lkdr.FiscalDataOut{
		"r1": {
			BuyerAddress:            "г. Москва",
			DateTime:                lkdr.DateTime(r1Time),
			FiscalDocumentFormatVer: "1.05",
			FiscalDocumentNumber:    1,
			FiscalDriveNumber:       "drv-1",
			FiscalSign:              "sign-1",
			KktRegId:                "kkt-1",
			OperationType:           1,
			PrepaidSum:              0,
			RequestNumber:           1,
			RetailPlace:             pointer.To("Магазин у дома"),
			RetailPlaceAddress:      pointer.To("г. Москва, ул. Тестовая, 1"),
			ShiftNumber:             10,
			TaxationType:            1,
			TotalSum:                1250.50,
			User:                    pointer.To("ООО «Магазин у дома»"),
			UserInn:                 "7700000001",
			Items: []lkdr.FiscalDataItem{
				{Name: "Молоко 3.2% 1л", Nds: 10, PaymentType: 4, Price: 89.90, ProductType: 1, Quantity: 2, Sum: 179.80},
				{Name: "Хлеб бородинский 400г", Nds: 10, PaymentType: 4, Price: 58.50, ProductType: 1, Quantity: 1, Sum: 58.50},
			},
		},
		"r2": {
			BuyerAddress:            "г. Москва",
			DateTime:                lkdr.DateTime(r2Time),
			FiscalDocumentFormatVer: "1.05",
			FiscalDocumentNumber:    2,
			FiscalDriveNumber:       "drv-2",
			FiscalSign:              "sign-2",
			KktRegId:                "kkt-2",
			OperationType:           1,
			PrepaidSum:              0,
			RequestNumber:           2,
			RetailPlace:             pointer.To("Кофейня №7"),
			RetailPlaceAddress:      pointer.To("г. Москва, ул. Тестовая, 2"),
			ShiftNumber:             20,
			TaxationType:            1,
			TotalSum:                480.00,
			User:                    pointer.To("ООО «Кофейня №7»"),
			UserInn:                 "7700000002",
			Items: []lkdr.FiscalDataItem{
				{Name: "Капучино 0.3л", Nds: 20, PaymentType: 4, Price: 240.00, ProductType: 1, Quantity: 1, Sum: 240.00},
			},
		},
		"r3": {
			BuyerAddress:            "г. Москва",
			DateTime:                lkdr.DateTime(r3Time),
			FiscalDocumentFormatVer: "1.05",
			FiscalDocumentNumber:    3,
			FiscalDriveNumber:       "drv-3",
			FiscalSign:              "sign-3",
			KktRegId:                "kkt-3",
			OperationType:           2,
			PrepaidSum:              0,
			RequestNumber:           3,
			RetailPlace:             pointer.To("Магазин у дома"),
			RetailPlaceAddress:      pointer.To("г. Москва, ул. Тестовая, 1"),
			ShiftNumber:             30,
			TaxationType:            1,
			TotalSum:                2390.10,
			User:                    pointer.To("ООО «Магазин у дома»"),
			UserInn:                 "7700000001",
			Items: []lkdr.FiscalDataItem{
				{Name: "Сыр 200г", Nds: 10, PaymentType: 4, Price: 410.55, ProductType: 1, Quantity: 2, Sum: 821.10},
				{Name: "Кофе в зернах 250г", Nds: 10, PaymentType: 4, Price: 990.00, ProductType: 1, Quantity: 1, Sum: 990.00},
				{Name: "Пакет-майка", Nds: 20, PaymentType: 4, Price: 9.00, ProductType: 1, Quantity: 1, Sum: 9.00},
			},
		},
	}
)
